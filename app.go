package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/crypto/ssh"

	"aimuxterm/acp"
	"aimuxterm/config"
	"aimuxterm/lsp"
	"aimuxterm/scanner"
	"aimuxterm/snapshot"
	"aimuxterm/terminal"
	"aimuxterm/watcher"
)

// remoteFileEntry holds file metadata for remote workspaces.
// Used for lightweight change detection without downloading file content.
type remoteFileEntry struct {
	path    string
	size    int64
	modTime time.Time
}

type WorkspaceSearchMatch struct {
	Line   int    `json:"line"`
	Column int    `json:"column"`
	Text   string `json:"text"`
	Match  string `json:"match"`
}

type WorkspaceSearchResult struct {
	Path    string                 `json:"path"`
	Matches []WorkspaceSearchMatch `json:"matches"`
}

func (e remoteFileEntry) fingerprint() string {
	return fmt.Sprintf("%d|%d", e.size, e.modTime.Unix())
}

func entriesToPaths(entries []remoteFileEntry) []string {
	paths := make([]string, len(entries))
	for i, e := range entries {
		paths[i] = e.path
	}
	return paths
}

func entriesToFingerprints(entries []remoteFileEntry) map[string]string {
	m := make(map[string]string, len(entries))
	for _, e := range entries {
		m[e.path] = e.fingerprint()
	}
	return m
}

// Remote file filters — mirrors scanner/scanner.go logic.
var remoteSkipDirs = map[string]bool{
	".git": true, "node_modules": true, ".warp-snapshots": true,
	"dist": true, "build": true, ".next": true, "__pycache__": true,
	"target": true, ".cache": true, "vendor": true, ".yarn": true,
	".pnpm-store": true, "bower_components": true, ".turbo": true,
	".nuxt": true, ".output": true, "coverage": true, ".nyc_output": true,
}

// isRemoteHidden reports whether a remote entry is structural noise: a skipped
// directory, a dotfile, or a .gitignore match. It says nothing about content type.
func (a *App) isRemoteHidden(relPath string, isDir bool) bool {
	name := path.Base(relPath)
	if isDir {
		if remoteSkipDirs[name] || (strings.HasPrefix(name, ".") && name != ".gitignore") {
			return true
		}
	}
	for _, seg := range strings.Split(relPath, "/") {
		if remoteSkipDirs[seg] || (strings.HasPrefix(seg, ".") && seg != ".." && seg != "." && seg != ".gitignore") {
			return true
		}
	}
	// Check .gitignore patterns
	if a.remoteGitignore != nil && a.remoteGitignore.Match(relPath) {
		return true
	}
	return false
}

// maxRemoteTextSize is the size limit above which a remote file is not snapshot-tracked.
const maxRemoteTextSize = 5 * 1024 * 1024

// isRemoteBinaryExt is an extension-only binary check for fast scanning (no download).
func isRemoteBinaryExt(relPath string) bool {
	ext := strings.ToLower(path.Ext(relPath))
	return !snapshot.IsTextFile(ext, nil)
}

// isRemoteNoise reports whether a remote file should be excluded from snapshot
// tracking: structural noise or binary content.
func (a *App) isRemoteNoise(relPath string, isDir bool) bool {
	if a.isRemoteHidden(relPath, isDir) {
		return true
	}
	return isRemoteBinaryExt(relPath)
}

// fingerprintFor returns the fingerprint for a file from the scanned remote entries.
func (a *App) fingerprintFor(relPath string) string {
	for _, e := range a.scannedRemoteEntries {
		if e.path == relPath {
			return e.fingerprint()
		}
	}
	return ""
}

// remoteIsBinary performs content-based binary check by reading the first bytes of a remote file.
func (a *App) remoteIsBinary(relPath string) bool {
	ext := strings.ToLower(path.Ext(relPath))
	rp := path.Join(a.remotePath, relPath)
	r, err := a.remoteSFTP.Open(rp)
	if err != nil {
		return true
	}
	defer r.Close()
	buf := make([]byte, 512)
	n, _ := r.Read(buf)
	return !snapshot.IsTextFile(ext, buf[:n])
}

// App is the main application struct with bound methods.
type App struct {
	ctx              context.Context
	workspace        string
	workspaceName    string
	startupWorkspace string
	isRemote         bool

	// Remote connection (lifetime = workspace session)
	remoteClient     *ssh.Client
	remoteSFTP       *sftp.Client
	remotePath       string
	remoteSSHCfg     terminal.SSHConfig // saved for auto-creating SSH terminals
	remotePollCancel context.CancelFunc
	remoteGitignore  *scanner.Gitignore

	snapEng  *snapshot.Engine
	termMgr  *terminal.Manager
	acpMgr   *acp.Manager
	fsw      *watcher.Watcher
	cfgStore *config.Store

	scannedFiles         []string
	scannedOtherFiles    []string
	scannedDirectories   []string
	scannedRemoteEntries []remoteFileEntry
	cachedChanges        []snapshot.FileChange
	changesCached        bool
	lspMgr               *lsp.Manager
	mu                   sync.Mutex
}

func NewApp() *App {
	store, err := config.NewStore()
	if err != nil {
		println("config store init failed:", err.Error())
		store = nil
	}
	app := &App{
		termMgr:  terminal.NewManager(),
		cfgStore: store,
	}
	app.acpMgr = acp.NewManager(func(ev acp.Event) {
		if app.ctx == nil {
			return
		}
		runtime.EventsEmit(app.ctx, "acp-event:"+ev.SessionID, ev)
	})
	app.lspMgr = lsp.NewManager(func(language string, body []byte) {
		if app.ctx != nil {
			runtime.EventsEmit(app.ctx, "lsp-message:"+language, string(body))
		}
	})
	return app
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

func (a *App) GetStartupWorkspace() string { return a.startupWorkspace }

func (a *App) OpenInNewWindow(path string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("找不到可执行文件: %w", err)
	}
	return exec.Command(exe, "--workspace", path).Start()
}

func (a *App) shutdown(ctx context.Context) {
	a.termMgr.CloseAll()
	if a.acpMgr != nil {
		a.acpMgr.CloseAll()
	}
	if a.lspMgr != nil {
		a.lspMgr.StopAll()
	}
	if a.fsw != nil {
		a.fsw.Close()
	}
	a.closeRemote()
}

// ensureGitignore makes sure .warp-snapshots is in the workspace .gitignore.
func ensureGitignore(workspace string) {
	giPath := filepath.Join(workspace, ".gitignore")
	data, err := os.ReadFile(giPath)
	if os.IsNotExist(err) {
		os.WriteFile(giPath, []byte(".warp-snapshots\n"), 0644)
		return
	}
	if err != nil {
		return
	}
	content := string(data)
	if !strings.Contains(content, ".warp-snapshots") {
		f, err := os.OpenFile(giPath, os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return
		}
		defer f.Close()
		if !strings.HasSuffix(content, "\n") {
			f.WriteString("\n")
		}
		f.WriteString(".warp-snapshots\n")
	}
}

func (a *App) remoteEnsureGitignore() {
	giPath := path.Join(a.remotePath, ".gitignore")
	data, err := a.readRemoteFileRaw(giPath)
	if err != nil {
		f, ferr := a.remoteSFTP.Create(giPath)
		if ferr != nil {
			return
		}
		defer f.Close()
		f.Write([]byte(".warp-snapshots\n"))
		return
	}
	content := string(data)
	if !strings.Contains(content, ".warp-snapshots") {
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += ".warp-snapshots\n"
		f, ferr := a.remoteSFTP.Create(giPath)
		if ferr != nil {
			return
		}
		defer f.Close()
		f.Write([]byte(content))
	}
}

func (a *App) closeRemote() {
	if a.remotePollCancel != nil {
		a.remotePollCancel()
		a.remotePollCancel = nil
	}
	if a.remoteSFTP != nil {
		a.remoteSFTP.Close()
		a.remoteSFTP = nil
	}
	if a.remoteClient != nil {
		a.remoteClient.Close()
		a.remoteClient = nil
	}
	a.isRemote = false
	a.remotePath = ""
	a.remoteGitignore = nil
	a.scannedRemoteEntries = nil
	a.cachedChanges = nil
	a.changesCached = false
}

// ─── Workspace ───────────────────────────────────────

type WorkspaceInfo struct {
	Path      string   `json:"path"`
	Name      string   `json:"name"`
	FileCount int      `json:"fileCount"`
	Files     []string `json:"files"`
	// OtherFiles lists files shown in the tree but never loaded: binary or oversized.
	OtherFiles   []string              `json:"otherFiles"`
	Directories  []string              `json:"directories"`
	IsRemote     bool                  `json:"isRemote"`
	ChangedFiles []snapshot.FileChange `json:"changedFiles"`
}

func (a *App) SelectWorkspace() (*WorkspaceInfo, error) {
	path, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择工作区文件夹",
	})
	if err != nil {
		return nil, fmt.Errorf("选择文件夹失败: %w", err)
	}
	if path == "" {
		return nil, nil
	}
	return a.OpenWorkspace(path)
}

func (a *App) GetLSPStatus(language string) lsp.ServerInfo {
	if a.isRemote || a.workspace == "" || a.lspMgr == nil {
		return lsp.ServerInfo{Language: language, Message: "LSP 仅支持本地工作区"}
	}
	return a.lspMgr.Status(language)
}

func (a *App) StartLSP(language string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.isRemote || a.workspace == "" {
		return fmt.Errorf("LSP 仅支持本地工作区")
	}
	if a.lspMgr == nil {
		return fmt.Errorf("LSP 管理器不可用")
	}
	return a.lspMgr.Start(language, a.workspace)
}

func (a *App) SendLSPMessage(language, message string) error {
	if a.lspMgr == nil {
		return fmt.Errorf("LSP 管理器不可用")
	}
	return a.lspMgr.Send(language, json.RawMessage(message))
}

func (a *App) StopLSP(language string) {
	if a.lspMgr != nil {
		a.lspMgr.Stop(language)
	}
}

func (a *App) OpenWorkspace(path string) (*WorkspaceInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closeRemote()

	if a.fsw != nil {
		a.fsw.Close()
	}

	a.workspace = path
	a.cachedChanges = nil
	a.changesCached = false
	ensureGitignore(path)
	a.snapEng = snapshot.NewEngine(path)

	result, err := scanner.Scan(path)
	if err != nil {
		return nil, fmt.Errorf("扫描失败: %w", err)
	}
	a.scannedFiles = result.Files
	a.scannedOtherFiles = result.OtherFiles
	a.scannedDirectories = result.Directories

	if err := a.snapEng.LoadManifest(); err != nil {
		return nil, fmt.Errorf("加载快照失败: %w", err)
	}
	if !a.snapEng.HasSnapshot() {
		if err := a.snapEng.Init(result.Files); err != nil {
			return nil, fmt.Errorf("创建快照失败: %w", err)
		}
	}

	a.fsw, err = watcher.New(path, func(events []string) { a.onFileChanged() })
	if err != nil {
		return nil, fmt.Errorf("启动文件监听失败: %w", err)
	}

	if a.cfgStore != nil {
		a.cfgStore.SaveWorkspace(path)
	}

	info := a.makeWorkspaceInfo()
	a.emitChanges()
	return info, nil
}

// RemoteDirEntry represents a single directory entry on the remote server.
// IsBinary marks entries whose content is never loaded (name-only display).
type RemoteDirEntry struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	IsDir    bool   `json:"isDir"`
	Size     int64  `json:"size"`
	ModTime  int64  `json:"modTime"`
	IsBinary bool   `json:"isBinary"`
}

// ─── Remote Workspace (SFTP Direct) ──────────────────

func (a *App) GetRemoteWorkspaces() ([]config.RemoteWorkspaceEntry, error) {
	if a.cfgStore == nil {
		return nil, nil
	}
	return a.cfgStore.LoadRemoteWorkspaces()
}

func (a *App) SaveRemoteWorkspace(entry config.RemoteWorkspaceEntry) error {
	if a.cfgStore == nil {
		return fmt.Errorf("配置存储不可用")
	}
	return a.cfgStore.SaveRemoteWorkspace(entry)
}

// ListRemoteDir lists entries in a single remote directory (lazy loading).
func (a *App) ListRemoteDir(dir string) ([]RemoteDirEntry, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.isRemote || a.remoteSFTP == nil {
		return nil, fmt.Errorf("当前不是远程工作区")
	}
	remoteDir := path.Join(a.remotePath, dir)
	if dir == "" {
		remoteDir = a.remotePath
	}
	infos, err := a.remoteSFTP.ReadDir(remoteDir)
	if err != nil {
		return nil, fmt.Errorf("读取远程目录失败: %w", err)
	}
	var entries []RemoteDirEntry
	for _, info := range infos {
		name := info.Name()
		if name == "." || name == ".." {
			continue
		}
		entryPath := path.Join(dir, name)
		if a.isRemoteHidden(entryPath, info.IsDir()) {
			continue
		}
		binary := !info.IsDir() && (isRemoteBinaryExt(entryPath) || info.Size() > maxRemoteTextSize)
		entries = append(entries, RemoteDirEntry{
			Name:     name,
			Path:     entryPath,
			IsDir:    info.IsDir(),
			Size:     info.Size(),
			ModTime:  info.ModTime().Unix(),
			IsBinary: binary,
		})
	}
	return entries, nil
}

func (a *App) RemoveRemoteWorkspace(name string) error {
	if a.cfgStore == nil {
		return nil
	}
	return a.cfgStore.RemoveRemoteWorkspace(name)
}

type SSHConfig struct {
	Name     string `json:"name"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	KeyPath  string `json:"keyPath"`
}

func (a *App) OpenRemoteWorkspace(cfg SSHConfig, remotePath string) (*WorkspaceInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closeRemote()
	if a.fsw != nil {
		a.fsw.Close()
		a.fsw = nil
	}

	if cfg.Port == 0 {
		cfg.Port = 22
	}
	if cfg.Password == "" && cfg.KeyPath == "" && a.cfgStore != nil {
		configs, err := a.cfgStore.LoadSSHConfigs()
		if err == nil {
			for _, c := range configs {
				if c.Name == cfg.Name || strings.HasPrefix(cfg.Name, c.Name+":") {
					cfg.Password = c.Password
					cfg.KeyPath = c.KeyPath
					break
				}
			}
		}
	}
	tCfg := terminal.SSHConfig{
		Name: cfg.Name, Host: cfg.Host, Port: cfg.Port,
		User: cfg.User, Password: cfg.Password, KeyPath: cfg.KeyPath,
	}
	auth, err := terminal.BuildSSHAuth(tCfg)
	if err != nil {
		return nil, err
	}
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	client, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            auth,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("SSH连接失败: %w", err)
	}

	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("SFTP初始化失败: %w", err)
	}

	a.remotePath = remotePath
	a.remoteSFTP = sftpClient
	a.remoteClient = client
	a.remoteSFTP = sftpClient
	a.remotePath = remotePath
	a.isRemote = true
	a.workspace = workspaceName(cfg.Name, remotePath)
	a.remoteSSHCfg = tCfg
	a.scannedOtherFiles = nil // remote tree loads lazily via ListRemoteDir
	a.snapEng = snapshot.NewEngine(remotePath)

	// Ensure .warp-snapshots/ exists on remote before scanning / snapshotting.
	snapDir := path.Join(a.remotePath, ".warp-snapshots")
	if err := sftpClient.MkdirAll(snapDir); err != nil {
		sftpClient.Close()
		client.Close()
		return nil, fmt.Errorf("创建远程快照目录失败: %w", err)
	}

	// Mutate .gitignore first so the subsequent scan baselines the post-fix mtime/size.
	a.remoteEnsureGitignore()

	if giData, err := a.readRemoteFileRaw(path.Join(remotePath, ".gitignore")); err == nil {
		a.remoteGitignore = scanner.ParseGitignore(string(giData))
	} else {
		a.remoteGitignore = &scanner.Gitignore{}
	}

	// Full Walk for change detection (noise-filtered, fast with skip dirs)
	entries, err := a.listRemoteFiles(sftpClient, remotePath)
	if err != nil {
		sftpClient.Close()
		client.Close()
		return nil, fmt.Errorf("扫描远程目录失败: %w", err)
	}
	a.scannedRemoteEntries = entries
	a.scannedFiles = entriesToPaths(entries)

	// Load manifest from remote; if absent init fresh
	if err := a.remoteLoadManifest(); err != nil {
		sftpClient.Close()
		client.Close()
		return nil, fmt.Errorf("加载远程清单失败: %w", err)
	}
	if !a.snapEng.HasSnapshot() {
		if err := a.remoteInitSnapshots(entries); err != nil {
			sftpClient.Close()
			client.Close()
			return nil, fmt.Errorf("创建远程快照失败: %w", err)
		}
	} else {
		// Clean stale manifest entries (now filtered by gitignore/binary/size)
		currentSet := make(map[string]bool, len(entries))
		for _, e := range entries {
			currentSet[e.path] = true
		}
		a.snapEng.FilterManifest(func(p string) bool { return currentSet[p] })
		for _, e := range entries {
			if _, ok := a.snapEng.GetFileFingerprint(e.path); !ok {
				a.snapEng.SetFileFingerprint(e.path, e.fingerprint())
			}
		}
		a.remoteSaveManifest()
	}

	// Save entry
	if a.cfgStore != nil {
		a.cfgStore.SaveRemoteWorkspace(config.RemoteWorkspaceEntry{
			Name:       workspaceName(cfg.Name, remotePath),
			Host:       cfg.Host,
			Port:       cfg.Port,
			User:       cfg.User,
			RemotePath: remotePath,
		})
	}

	info := a.makeWorkspaceInfo()
	a.emitChanges()
	pollCtx, cancel := context.WithCancel(a.ctx)
	a.remotePollCancel = cancel
	go a.remotePollLoop(pollCtx)

	return info, nil
}

func (a *App) RefreshLocalWorkspace() (*WorkspaceInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.isRemote || a.workspace == "" {
		return nil, fmt.Errorf("当前不是本地工作区")
	}
	a.refreshScanLocked()
	a.emitChanges()
	return a.makeWorkspaceInfo(), nil
}

func (a *App) RefreshRemoteWorkspace() (*WorkspaceInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.isRemote || a.remoteSFTP == nil {
		return nil, fmt.Errorf("当前不是远程工作区")
	}
	entries, err := a.listRemoteFiles(a.remoteSFTP, a.remotePath)
	if err != nil {
		return nil, err
	}
	a.scannedRemoteEntries = entries
	a.scannedFiles = entriesToPaths(entries)
	a.cachedChanges = nil
	a.changesCached = false
	info := a.makeWorkspaceInfo()
	a.emitChanges()
	return info, nil
}

func (a *App) listRemoteFiles(c *sftp.Client, root string) ([]remoteFileEntry, error) {
	var entries []remoteFileEntry
	w := c.Walk(root)
	for w.Step() {
		if w.Err() != nil {
			continue
		}
		s := w.Stat()
		if s == nil || s.IsDir() {
			continue
		}
		rel := strings.TrimPrefix(path.Clean(w.Path()), path.Clean(root))
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" || a.isRemoteNoise(rel, false) || s.Size() > maxRemoteTextSize {
			continue
		}
		entries = append(entries, remoteFileEntry{
			path:    filepath.ToSlash(rel),
			size:    s.Size(),
			modTime: s.ModTime(),
		})
	}
	return entries, nil
}

func (a *App) readRemoteFile(relPath string) ([]byte, error) {
	rp := path.Join(a.remotePath, relPath)
	r, err := a.remoteSFTP.Open(rp)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// ─── Remote Snapshot Helpers ─────────────────────────

func (a *App) remoteSnapPath(relPath string) string {
	return path.Join(a.remotePath, ".warp-snapshots", relPath)
}

func (a *App) remoteObjectPath(hash string) string {
	if len(hash) < 4 {
		return ""
	}
	return path.Join(a.remotePath, ".warp-snapshots", "objects", hash[:2], hash[2:])
}

func (a *App) remoteHasObject(hash string) bool {
	rp := a.remoteObjectPath(hash)
	if rp == "" {
		return false
	}
	_, err := a.remoteSFTP.Stat(rp)
	return err == nil
}

func (a *App) remoteWriteObject(hash string, data []byte) error {
	if a.remoteHasObject(hash) {
		return nil // dedup
	}
	rp := a.remoteObjectPath(hash)
	if rp == "" {
		return fmt.Errorf("invalid hash")
	}
	if err := a.remoteSFTP.MkdirAll(path.Dir(rp)); err != nil {
		return err
	}
	f, err := a.remoteSFTP.Create(rp)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(snapshot.Compress(data))
	return err
}

func (a *App) remoteReadObject(hash string) ([]byte, error) {
	rp := a.remoteObjectPath(hash)
	if rp == "" {
		return nil, fmt.Errorf("invalid hash")
	}
	r, err := a.remoteSFTP.Open(rp)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return snapshot.Decompress(raw), nil
}

// remoteReadSnapshotByPath reads a snapshot file, trying object storage first.
func (a *App) remoteReadSnapshotByPath(relPath string) ([]byte, error) {
	if a.snapEng != nil {
		if hash, ok := a.snapEng.GetFileHash(relPath); ok && len(hash) >= 4 {
			if data, err := a.remoteReadObject(hash); err == nil {
				return data, nil
			}
		}
	}
	// Fall back to old path-based layout
	r, err := a.remoteSFTP.Open(a.remoteSnapPath(relPath))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

func (a *App) remoteWriteSnapshot(relPath string, data []byte) error {
	h := snapshot.HashBytes(data)
	if err := a.remoteWriteObject(h, data); err != nil {
		return err
	}
	if a.snapEng != nil {
		a.snapEng.SetFileHash(relPath, h)
	}
	return nil
}

func (a *App) remoteRemoveSnapshot(relPath string) error {
	return a.remoteSFTP.Remove(a.remoteSnapPath(relPath))
}

func (a *App) remoteRemoveSnapshotDir() {
	// best-effort cleanup
	a.remoteSFTP.RemoveDirectory(path.Join(a.remotePath, ".warp-snapshots"))
}

func (a *App) remoteLoadManifest() error {
	rp := path.Join(a.remotePath, ".warp-snapshots", "manifest.json")
	data, err := a.readRemoteFileRaw(rp)
	if err != nil {
		// If manifest doesn't exist on remote, start fresh
		a.snapEng = snapshot.NewEngine(a.workspace)
		return nil
	}
	a.snapEng = snapshot.NewEngine(a.workspace)
	return a.snapEng.LoadManifestFrom(data)
}

func (a *App) remoteSaveManifest() error {
	data, err := a.snapEng.MarshalManifest()
	if err != nil {
		return err
	}
	rp := path.Join(a.remotePath, ".warp-snapshots", "manifest.json")
	a.remoteSFTP.MkdirAll(path.Dir(rp))
	f, err := a.remoteSFTP.Create(rp)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

// readRemoteFileRaw reads a file by its full remote path (not relative to workspace).
func (a *App) readRemoteFileRaw(fullPath string) ([]byte, error) {
	r, err := a.remoteSFTP.Open(fullPath)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// remoteInitSnapshots copies text files to remote .warp-snapshots using server-side
// copy via SSH exec. Falls back to per-file SFTP if SSH exec is unavailable.
func (a *App) remoteInitSnapshots(entries []remoteFileEntry) error {
	type textEntry struct {
		path string
		fp   string
	}
	var textEntries []textEntry
	for _, e := range entries {
		ext := strings.ToLower(path.Ext(e.path))
		if !snapshot.IsTextFile(ext, nil) {
			continue
		}
		textEntries = append(textEntries, textEntry{path: e.path, fp: e.fingerprint()})
	}

	if len(textEntries) == 0 {
		return a.remoteSaveManifest()
	}

	runtime.EventsEmit(a.ctx, "snapshot-progress", map[string]interface{}{
		"phase": "start", "total": len(textEntries), "current": 0,
	})

	textPaths := make([]string, len(textEntries))
	for i, te := range textEntries {
		textPaths[i] = te.path
	}

	chunkSize := 1000
	for i := 0; i < len(textPaths); i += chunkSize {
		end := i + chunkSize
		if end > len(textPaths) {
			end = len(textPaths)
		}
		chunk := textPaths[i:end]
		mapping, err := a.remoteExecCopyChunk(chunk)
		if err != nil {
			for _, te := range textEntries[i:end] {
				data, err := a.readRemoteFile(te.path)
				if err != nil {
					continue
				}
				if err := a.remoteWriteSnapshot(te.path, data); err != nil {
					return err
				}
				a.snapEng.SetFileFingerprint(te.path, te.fp)
			}
		} else {
			fpByPath := make(map[string]string, end-i)
			for _, te := range textEntries[i:end] {
				fpByPath[te.path] = te.fp
			}
			for pth, sha := range mapping {
				a.snapEng.SetFileHash(pth, sha)
				if fp, ok := fpByPath[pth]; ok {
					a.snapEng.SetFileFingerprint(pth, fp)
				}
			}
		}
		runtime.EventsEmit(a.ctx, "snapshot-progress", map[string]interface{}{
			"phase": "progress", "total": len(textPaths), "current": end,
		})
	}

	// Ensure every text file has a fingerprint so modification detection uses size|mtime.
	for _, te := range textEntries {
		if _, ok := a.snapEng.GetFileFingerprint(te.path); !ok {
			a.snapEng.SetFileFingerprint(te.path, te.fp)
		}
	}

	return a.remoteSaveManifest()
}

// remoteExecCopyChunk copies files server-side using sha256sum + object storage.
// Returns path→hash mapping parsed from stdout.
func (a *App) remoteExecCopyChunk(paths []string) (map[string]string, error) {
	sess, err := a.remoteClient.NewSession()
	if err != nil {
		return nil, err
	}
	defer sess.Close()

	script := "cd " + shellQuote(a.remotePath) + " || exit 1\n" +
		"mkdir -p .warp-snapshots/objects || exit 1\n" +
		"while IFS= read -r f; do\n" +
		"  [ -z \"$f\" ] && continue\n" +
		"  hash=$(sha256sum \"$f\" | awk '{print $1}')\n" +
		"  p1=\"${hash:0:2}\"\n" +
		"  p2=\"${hash:2}\"\n" +
		"  mkdir -p \".warp-snapshots/objects/$p1\"\n" +
		"  [ -f \".warp-snapshots/objects/$p1/$p2\" ] || gzip -c \"$f\" > \".warp-snapshots/objects/$p1/$p2\"\n" +
		"  echo \"$hash $f\"\n" +
		"done\n"

	sess.Stdin = strings.NewReader(strings.Join(paths, "\n") + "\n")
	output, err := sess.Output(script)
	if err != nil {
		return nil, err
	}
	mapping := make(map[string]string, len(paths))
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) == 2 {
			mapping[parts[1]] = parts[0]
		}
	}
	return mapping, nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func workspaceName(cfgName, remotePath string) string {
	if strings.HasSuffix(cfgName, ":"+remotePath) {
		return cfgName
	}
	return cfgName + ":" + remotePath
}

// remotePollLoop periodically re-scans the remote directory for changes.
func (a *App) remotePollLoop(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.remotePoll()
		}
	}
}

func (a *App) remotePoll() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.isRemote || a.remoteSFTP == nil {
		return
	}
	entries, err := a.listRemoteFiles(a.remoteSFTP, a.remotePath)
	if err != nil {
		return
	}
	oldFps := entriesToFingerprints(a.scannedRemoteEntries)
	newFps := entriesToFingerprints(entries)
	if fingerprintsEqual(oldFps, newFps) {
		return
	}
	a.scannedRemoteEntries = entries
	a.scannedFiles = entriesToPaths(entries)
	a.cachedChanges = nil
	a.changesCached = false
	a.emitChanges()
}

func fingerprintsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// remoteChangedFiles returns changes with line stats.
func (a *App) remoteChangedFiles() []snapshot.FileChange {
	changes := a.snapEng.ChangedFilesByHash(entriesToFingerprints(a.scannedRemoteEntries))
	for i, c := range changes {
		var oldData, newData []byte
		switch c.Status {
		case snapshot.StatusAdded:
			newData, _ = a.readRemoteFile(c.Path)
		case snapshot.StatusModified:
			oldData, _ = a.remoteReadSnapshotByPath(c.Path)
			newData, _ = a.readRemoteFile(c.Path)
		case snapshot.StatusDeleted:
			oldData, _ = a.remoteReadSnapshotByPath(c.Path)
		}
		changes[i].Additions, changes[i].Deletions = snapshot.DiffStats(oldData, newData)
	}
	return changes
}

func (a *App) GetWorkspaceHistory() []config.WorkspaceEntry {
	if a.cfgStore == nil {
		return nil
	}
	entries, _ := a.cfgStore.LoadWorkspaces()
	return entries
}

func (a *App) RemoveWorkspaceFromHistory(path string) error {
	if a.cfgStore == nil {
		return nil
	}
	return a.cfgStore.RemoveWorkspace(path)
}

func (a *App) GetWorkspaceInfo() *WorkspaceInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.workspace == "" || a.snapEng == nil {
		return nil
	}
	return a.makeWorkspaceInfo()
}

func (a *App) workspaceChangesLocked() []snapshot.FileChange {
	if a.changesCached {
		return a.cachedChanges
	}
	if a.isRemote {
		a.cachedChanges = a.remoteChangedFiles()
	} else {
		a.cachedChanges = a.snapEng.ChangedFiles(a.scannedFiles)
	}
	a.changesCached = true
	return a.cachedChanges
}

func (a *App) makeWorkspaceInfo() *WorkspaceInfo {
	changes := a.workspaceChangesLocked()
	name := a.workspace
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '\\' || name[i] == '/' {
			name = name[i+1:]
			break
		}
	}
	if a.isRemote {
		name = a.remotePath
		for i := len(name) - 1; i >= 0; i-- {
			if name[i] == '/' {
				name = name[i+1:]
				break
			}
		}
	}
	return &WorkspaceInfo{
		Path:         a.workspace,
		Name:         name,
		FileCount:    len(a.scannedFiles) + len(a.scannedOtherFiles),
		Files:        a.scannedFiles,
		OtherFiles:   a.scannedOtherFiles,
		Directories:  a.scannedDirectories,
		IsRemote:     a.isRemote,
		ChangedFiles: changes,
	}
}

func (a *App) onFileChanged() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.snapEng == nil {
		return
	}
	a.cachedChanges = nil
	a.changesCached = false
	changes := a.workspaceChangesLocked()
	runtime.EventsEmit(a.ctx, "file-changes", changes)
}

// ─── File Changes ────────────────────────────────────

func (a *App) GetChangedFiles() []snapshot.FileChange {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.snapEng == nil {
		return nil
	}
	return a.workspaceChangesLocked()
}

func (a *App) AcceptAll() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.snapEng == nil {
		return fmt.Errorf("未选择工作区")
	}
	if a.isRemote {
		fps := entriesToFingerprints(a.scannedRemoteEntries)
		changes := a.snapEng.ChangedFilesByHash(fps)
		for _, c := range changes {
			if c.Status == snapshot.StatusDeleted {
				_ = a.remoteRemoveSnapshot(c.Path)
				_ = a.snapEng.RemoveFromManifest([]string{c.Path})
				continue
			}
			fp := fps[c.Path]
			if a.remoteIsBinary(c.Path) {
				if fp != "" {
					a.snapEng.SetFileFingerprint(c.Path, fp)
				}
				continue
			}
			data, err := a.readRemoteFile(c.Path)
			if err != nil {
				// File vanished between scan and accept — drop baseline entry.
				_ = a.remoteRemoveSnapshot(c.Path)
				_ = a.snapEng.RemoveFromManifest([]string{c.Path})
				continue
			}
			if err := a.remoteWriteSnapshot(c.Path, data); err != nil {
				return err
			}
			if fp != "" {
				a.snapEng.SetFileFingerprint(c.Path, fp)
			}
		}
		if err := a.remoteSaveManifest(); err != nil {
			return err
		}
		a.emitChanges()
		return nil
	}
	changes := a.snapEng.ChangedFiles(a.scannedFiles)
	paths := make([]string, len(changes))
	for i, c := range changes {
		paths[i] = c.Path
	}
	if err := a.snapEng.AcceptAll(paths); err != nil {
		return err
	}
	a.refreshScanLocked()
	a.emitChanges()
	return nil
}

func (a *App) RevertAll() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.snapEng == nil {
		return fmt.Errorf("未选择工作区")
	}
	if a.isRemote {
		changes := a.snapEng.ChangedFilesByHash(entriesToFingerprints(a.scannedRemoteEntries))
		for _, c := range changes {
			snapData, err := a.remoteReadSnapshotByPath(c.Path)
			if err != nil {
				// Added file or missing baseline: remove working copy and drop manifest entry.
				_ = a.remoteSFTP.Remove(path.Join(a.remotePath, c.Path))
				_ = a.snapEng.RemoveFromManifest([]string{c.Path})
				continue
			}
			if c.Status == snapshot.StatusDeleted || c.Status == snapshot.StatusModified || c.Status == snapshot.StatusAdded {
				rp := path.Join(a.remotePath, c.Path)
				if dir := path.Dir(rp); dir != "." && dir != "/" {
					_ = a.remoteSFTP.MkdirAll(dir)
				}
				f, err := a.remoteSFTP.Create(rp)
				if err != nil {
					return err
				}
				if _, err := f.Write(snapData); err != nil {
					f.Close()
					return err
				}
				f.Close()
			}
		}
		a.refreshScanLocked()
		fps := entriesToFingerprints(a.scannedRemoteEntries)
		for pth, fp := range fps {
			a.snapEng.SetFileFingerprint(pth, fp)
		}
		if err := a.remoteSaveManifest(); err != nil {
			return err
		}
		a.emitChanges()
		return nil
	}
	changes := a.snapEng.ChangedFiles(a.scannedFiles)
	paths := make([]string, len(changes))
	for i, c := range changes {
		paths[i] = c.Path
	}
	if err := a.snapEng.RevertAll(paths); err != nil {
		return err
	}
	a.refreshScanLocked()
	a.emitChanges()
	return nil
}

func (a *App) AcceptFile(p string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.snapEng == nil {
		return fmt.Errorf("未选择工作区")
	}
	if a.isRemote {
		data, err := a.readRemoteFile(p)
		if err != nil {
			_ = a.remoteRemoveSnapshot(p)
			_ = a.snapEng.RemoveFromManifest([]string{p})
			if err := a.remoteSaveManifest(); err != nil {
				return err
			}
			a.emitChanges()
			return nil
		}
		ext := strings.ToLower(path.Ext(p))
		fp := a.fingerprintFor(p)
		if !snapshot.IsTextFile(ext, snapshot.FirstBytes(data)) {
			if fp != "" {
				a.snapEng.SetFileFingerprint(p, fp)
			}
			if err := a.remoteSaveManifest(); err != nil {
				return err
			}
			a.emitChanges()
			return nil
		}
		if err := a.remoteWriteSnapshot(p, data); err != nil {
			return err
		}
		if fp != "" {
			a.snapEng.SetFileFingerprint(p, fp)
		}
		if err := a.remoteSaveManifest(); err != nil {
			return err
		}
		a.emitChanges()
		return nil
	}
	if err := a.snapEng.AcceptFile(p); err != nil {
		return err
	}
	a.refreshScanLocked()
	a.emitChanges()
	return nil
}

func (a *App) RevertFile(p string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.snapEng == nil {
		return fmt.Errorf("未选择工作区")
	}
	if a.isRemote {
		snapData, err := a.remoteReadSnapshotByPath(p)
		if err != nil {
			// No baseline snapshot: treat as added file and remove it.
			_ = a.remoteSFTP.Remove(path.Join(a.remotePath, p))
			_ = a.snapEng.RemoveFromManifest([]string{p})
			a.refreshScanLocked()
			if err := a.remoteSaveManifest(); err != nil {
				return err
			}
			a.emitChanges()
			return nil
		}
		rp := path.Join(a.remotePath, p)
		a.remoteSFTP.MkdirAll(path.Dir(rp))
		f, err := a.remoteSFTP.Create(rp)
		if err != nil {
			return err
		}
		if _, err := f.Write(snapData); err != nil {
			f.Close()
			return err
		}
		f.Close()
		a.refreshScanLocked()
		if fp := a.fingerprintFor(p); fp != "" {
			a.snapEng.SetFileFingerprint(p, fp)
		}
		if err := a.remoteSaveManifest(); err != nil {
			return err
		}
		a.emitChanges()
		return nil
	}
	if err := a.snapEng.RevertFile(p); err != nil {
		return err
	}
	a.refreshScanLocked()
	a.emitChanges()
	return nil
}

func (a *App) GetFileDiff(path string) (map[string]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.snapEng == nil {
		return nil, fmt.Errorf("未选择工作区")
	}
	if a.isRemote {
		newData, newErr := a.readRemoteFile(path)
		oldData, oldErr := a.remoteReadSnapshotByPath(path)
		if newErr != nil && oldErr != nil {
			return nil, newErr
		}
		if newErr != nil {
			newData = nil // deleted file
		}
		if oldErr != nil {
			oldData = nil // added file, no snapshot
		}
		return map[string]string{
			"old": string(oldData),
			"new": string(newData),
		}, nil
	}
	oldC, newC, err := a.snapEng.Diff(path)
	if err != nil {
		return nil, err
	}
	return map[string]string{"old": oldC, "new": newC}, nil
}

func (a *App) GetFileContent(path string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.workspace == "" || a.snapEng == nil {
		return "", fmt.Errorf("未选择工作区")
	}
	// Never load content for files the scanner classified as non-text.
	for _, other := range a.scannedOtherFiles {
		if other == path {
			return "", fmt.Errorf("二进制或超大文件，不支持预览")
		}
	}
	if a.isRemote {
		data, err := a.readRemoteFile(path)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	return snapshot.ReadFileContent(a.workspace, path)
}

func (a *App) SaveFile(relPath, content string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.workspace == "" || a.snapEng == nil {
		return fmt.Errorf("未选择工作区")
	}
	if a.isRemote {
		if a.remoteSFTP == nil {
			return fmt.Errorf("远程连接不可用")
		}
		rp := path.Join(a.remotePath, relPath)
		a.remoteSFTP.MkdirAll(path.Dir(rp))
		f, err := a.remoteSFTP.Create(rp)
		if err != nil {
			return fmt.Errorf("写入远程文件失败: %w", err)
		}
		defer f.Close()
		if _, err := f.Write([]byte(content)); err != nil {
			return fmt.Errorf("写入远程文件失败: %w", err)
		}
		f.Close()
		a.remoteWriteSnapshot(relPath, []byte(content))
		a.refreshScanLocked()
		a.snapEng.SetFileFingerprint(relPath, a.fingerprintFor(relPath))
		if err := a.remoteSaveManifest(); err != nil {
			return fmt.Errorf("更新清单失败: %w", err)
		}
		a.emitChanges()
		return nil
	}
	fullPath := filepath.Join(a.workspace, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("保存文件失败: %w", err)
	}
	a.refreshScanLocked()
	a.emitChanges()
	return nil
}

func (a *App) SearchWorkspace(query string, matchCase bool) ([]WorkspaceSearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []WorkspaceSearchResult{}, nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.workspace == "" || a.snapEng == nil {
		return nil, fmt.Errorf("未选择工作区")
	}

	paths := append([]string(nil), a.scannedFiles...)
	sort.Strings(paths)
	const maxSearchFiles = 200
	const maxSearchMatches = 2000
	results := make([]WorkspaceSearchResult, 0)
	matchCount := 0
	for _, relPath := range paths {
		if len(results) >= maxSearchFiles || matchCount >= maxSearchMatches {
			break
		}
		data, err := a.workspaceSearchFileContent(relPath)
		if err != nil {
			continue
		}
		matches := workspaceSearchMatches(string(data), query, matchCase, maxSearchMatches-matchCount)
		if len(matches) == 0 {
			continue
		}
		results = append(results, WorkspaceSearchResult{Path: relPath, Matches: matches})
		matchCount += len(matches)
	}
	return results, nil
}

func (a *App) ReplaceWorkspace(query, replacement string, matchCase bool) ([]string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("搜索内容不能为空")
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.workspace == "" || a.snapEng == nil {
		return nil, fmt.Errorf("未选择工作区")
	}

	paths := append([]string(nil), a.scannedFiles...)
	sort.Strings(paths)
	changed := make([]string, 0)
	for _, relPath := range paths {
		data, err := a.workspaceSearchFileContent(relPath)
		if err != nil {
			continue
		}
		content := string(data)
		updated, count := workspaceReplaceAll(content, query, replacement, matchCase)
		if count == 0 {
			continue
		}
		if err := a.writeWorkspaceSearchFile(relPath, []byte(updated)); err != nil {
			return nil, err
		}
		changed = append(changed, relPath)
	}
	if len(changed) == 0 {
		return changed, nil
	}
	if a.isRemote {
		for _, relPath := range changed {
			data, err := a.workspaceSearchFileContent(relPath)
			if err != nil {
				return nil, err
			}
			if err := a.remoteWriteSnapshot(relPath, data); err != nil {
				return nil, err
			}
		}
	}
	if err := a.refreshWorkspaceAfterFileOperation(); err != nil {
		return nil, err
	}
	if a.isRemote {
		if err := a.remoteSaveManifest(); err != nil {
			return nil, fmt.Errorf("更新清单失败: %w", err)
		}
	}
	return changed, nil
}

func (a *App) workspaceSearchFileContent(relPath string) ([]byte, error) {
	if a.isRemote {
		return a.readRemoteFile(relPath)
	}
	return os.ReadFile(filepath.Join(a.workspace, filepath.FromSlash(relPath)))
}

func (a *App) writeWorkspaceSearchFile(relPath string, data []byte) error {
	if a.isRemote {
		if a.remoteSFTP == nil {
			return fmt.Errorf("远程连接不可用")
		}
		remotePath := path.Join(a.remotePath, relPath)
		file, err := a.remoteSFTP.Create(remotePath)
		if err != nil {
			return fmt.Errorf("写入远程文件失败: %w", err)
		}
		if _, err := file.Write(data); err != nil {
			file.Close()
			return fmt.Errorf("写入远程文件失败: %w", err)
		}
		return file.Close()
	}
	if err := os.WriteFile(filepath.Join(a.workspace, filepath.FromSlash(relPath)), data, 0644); err != nil {
		return fmt.Errorf("保存文件失败: %w", err)
	}
	return nil
}

func workspaceSearchMatches(content, query string, matchCase bool, limit int) []WorkspaceSearchMatch {
	needle := query
	if !matchCase {
		needle = strings.ToLower(needle)
	}
	matches := make([]WorkspaceSearchMatch, 0)
	lineNumber := 1
	for _, line := range strings.Split(content, "\n") {
		searchLine := line
		if !matchCase {
			searchLine = strings.ToLower(line)
		}
		from := 0
		for {
			index := strings.Index(searchLine[from:], needle)
			if index < 0 {
				break
			}
			index += from
			matches = append(matches, WorkspaceSearchMatch{Line: lineNumber, Column: len([]rune(line[:index])) + 1, Text: line, Match: line[index : index+len(query)]})
			if len(matches) >= limit {
				return matches
			}
			from = index + len(needle)
		}
		lineNumber++
	}
	return matches
}

func workspaceReplaceAll(content, query, replacement string, matchCase bool) (string, int) {
	if matchCase {
		count := strings.Count(content, query)
		return strings.ReplaceAll(content, query, replacement), count
	}
	lowerContent := strings.ToLower(content)
	lowerQuery := strings.ToLower(query)
	var result strings.Builder
	count, from := 0, 0
	for {
		index := strings.Index(lowerContent[from:], lowerQuery)
		if index < 0 {
			result.WriteString(content[from:])
			break
		}
		index += from
		result.WriteString(content[from:index])
		result.WriteString(replacement)
		from = index + len(query)
		count++
	}
	return result.String(), count
}

func (a *App) DeleteWorkspaceFile(relPath string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.workspace == "" || a.snapEng == nil {
		return fmt.Errorf("未选择工作区")
	}
	relPath = filepath.ToSlash(filepath.Clean(relPath))
	if relPath == "." || strings.HasPrefix(relPath, "../") || filepath.IsAbs(relPath) {
		return fmt.Errorf("无效的文件路径")
	}
	if a.isRemote {
		if a.remoteSFTP == nil {
			return fmt.Errorf("远程连接不可用")
		}
		if err := a.remoteSFTP.RemoveAll(path.Join(a.remotePath, relPath)); err != nil {
			return fmt.Errorf("删除远程文件或文件夹失败: %w", err)
		}
		entries, err := a.listRemoteFiles(a.remoteSFTP, a.remotePath)
		if err != nil {
			return err
		}
		a.scannedRemoteEntries = entries
		a.scannedFiles = entriesToPaths(entries)
		a.emitChanges()
		return nil
	}
	if err := os.RemoveAll(filepath.Join(a.workspace, relPath)); err != nil {
		return fmt.Errorf("删除文件或文件夹失败: %w", err)
	}
	a.refreshScanLocked()
	a.emitChanges()
	return nil
}

func (a *App) UploadWorkspaceFiles(targetDir string) error {
	picked, err := runtime.OpenMultipleFilesDialog(a.ctx, runtime.OpenDialogOptions{Title: "上传文件到工作区"})
	if err != nil {
		return fmt.Errorf("选择上传文件失败: %w", err)
	}
	if len(picked) == 0 {
		return nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.workspace == "" || a.snapEng == nil {
		return fmt.Errorf("未选择工作区")
	}
	targetDir = filepath.ToSlash(filepath.Clean(targetDir))
	if targetDir == "." {
		targetDir = ""
	}
	if strings.HasPrefix(targetDir, "../") || filepath.IsAbs(targetDir) {
		return fmt.Errorf("无效的目标目录")
	}
	for _, source := range picked {
		data, err := os.ReadFile(source)
		if err != nil {
			return fmt.Errorf("读取上传文件失败: %w", err)
		}
		name := filepath.Base(source)
		if a.isRemote {
			if a.remoteSFTP == nil {
				return fmt.Errorf("远程连接不可用")
			}
			destination := path.Join(a.remotePath, filepath.ToSlash(targetDir), name)
			if err := a.remoteSFTP.MkdirAll(path.Dir(destination)); err != nil {
				return fmt.Errorf("创建远程目录失败: %w", err)
			}
			file, err := a.remoteSFTP.Create(destination)
			if err != nil {
				return fmt.Errorf("创建远程文件失败: %w", err)
			}
			_, writeErr := file.Write(data)
			closeErr := file.Close()
			if writeErr != nil {
				return fmt.Errorf("上传远程文件失败: %w", writeErr)
			}
			if closeErr != nil {
				return fmt.Errorf("关闭远程文件失败: %w", closeErr)
			}
			continue
		}
		destination := filepath.Join(a.workspace, targetDir, name)
		if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
			return fmt.Errorf("创建目录失败: %w", err)
		}
		if err := os.WriteFile(destination, data, 0644); err != nil {
			return fmt.Errorf("上传文件失败: %w", err)
		}
	}
	if a.isRemote {
		entries, err := a.listRemoteFiles(a.remoteSFTP, a.remotePath)
		if err != nil {
			return err
		}
		a.scannedRemoteEntries = entries
		a.scannedFiles = entriesToPaths(entries)
	} else {
		a.refreshScanLocked()
	}
	a.emitChanges()
	return nil
}

func (a *App) workspaceRelativePath(relPath string, allowRoot bool) (string, error) {
	relPath = filepath.ToSlash(filepath.Clean(relPath))
	if relPath == "." && allowRoot {
		return "", nil
	}
	if relPath == "." || strings.HasPrefix(relPath, "../") || filepath.IsAbs(relPath) {
		return "", fmt.Errorf("无效的工作区路径")
	}
	return relPath, nil
}

func (a *App) refreshWorkspaceAfterFileOperation() error {
	if a.isRemote {
		entries, err := a.listRemoteFiles(a.remoteSFTP, a.remotePath)
		if err != nil {
			return err
		}
		a.scannedRemoteEntries = entries
		a.scannedFiles = entriesToPaths(entries)
	} else {
		a.refreshScanLocked()
	}
	a.emitChanges()
	return nil
}

// CreateWorkspaceFile creates an empty file relative to the current workspace.
func (a *App) CreateWorkspaceFile(relPath string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.workspace == "" || a.snapEng == nil {
		return fmt.Errorf("未选择工作区")
	}
	relPath, err := a.workspaceRelativePath(relPath, false)
	if err != nil {
		return err
	}
	if a.isRemote {
		if a.remoteSFTP == nil {
			return fmt.Errorf("远程连接不可用")
		}
		fullPath := path.Join(a.remotePath, relPath)
		if _, err := a.remoteSFTP.Stat(fullPath); err == nil {
			return fmt.Errorf("文件已存在")
		}
		if err := a.remoteSFTP.MkdirAll(path.Dir(fullPath)); err != nil {
			return fmt.Errorf("创建远程目录失败: %w", err)
		}
		f, err := a.remoteSFTP.Create(fullPath)
		if err != nil {
			return fmt.Errorf("创建远程文件失败: %w", err)
		}
		if err := f.Close(); err != nil {
			return err
		}
		return a.refreshWorkspaceAfterFileOperation()
	}
	fullPath := filepath.Join(a.workspace, relPath)
	if _, err := os.Stat(fullPath); err == nil {
		return fmt.Errorf("文件已存在")
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}
	if err := os.WriteFile(fullPath, nil, 0644); err != nil {
		return fmt.Errorf("创建文件失败: %w", err)
	}
	return a.refreshWorkspaceAfterFileOperation()
}

// CreateWorkspaceFolder creates a folder relative to the current workspace.
func (a *App) CreateWorkspaceFolder(relPath string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.workspace == "" || a.snapEng == nil {
		return fmt.Errorf("未选择工作区")
	}
	relPath, err := a.workspaceRelativePath(relPath, false)
	if err != nil {
		return err
	}
	if a.isRemote {
		if a.remoteSFTP == nil {
			return fmt.Errorf("远程连接不可用")
		}
		fullPath := path.Join(a.remotePath, relPath)
		if _, err := a.remoteSFTP.Stat(fullPath); err == nil {
			return fmt.Errorf("文件夹已存在")
		}
		if err := a.remoteSFTP.MkdirAll(fullPath); err != nil {
			return fmt.Errorf("创建远程文件夹失败: %w", err)
		}
		return a.refreshWorkspaceAfterFileOperation()
	}
	fullPath := filepath.Join(a.workspace, relPath)
	if _, err := os.Stat(fullPath); err == nil {
		return fmt.Errorf("文件夹已存在")
	}
	if err := os.MkdirAll(fullPath, 0755); err != nil {
		return fmt.Errorf("创建文件夹失败: %w", err)
	}
	return a.refreshWorkspaceAfterFileOperation()
}

// RenameWorkspacePath renames one file or folder.
func (a *App) RenameWorkspacePath(oldPath, newPath string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.workspace == "" || a.snapEng == nil {
		return fmt.Errorf("未选择工作区")
	}
	oldPath, err := a.workspaceRelativePath(oldPath, false)
	if err != nil {
		return err
	}
	newPath, err = a.workspaceRelativePath(newPath, false)
	if err != nil {
		return err
	}
	if oldPath == newPath {
		return nil
	}
	if a.isRemote {
		if a.remoteSFTP == nil {
			return fmt.Errorf("远程连接不可用")
		}
		from, to := path.Join(a.remotePath, oldPath), path.Join(a.remotePath, newPath)
		if _, err := a.remoteSFTP.Stat(to); err == nil {
			return fmt.Errorf("目标路径已存在")
		}
		if err := a.remoteSFTP.MkdirAll(path.Dir(to)); err != nil {
			return err
		}
		if err := a.remoteSFTP.Rename(from, to); err != nil {
			return fmt.Errorf("重命名远程路径失败: %w", err)
		}
		return a.refreshWorkspaceAfterFileOperation()
	}
	from, to := filepath.Join(a.workspace, oldPath), filepath.Join(a.workspace, newPath)
	if _, err := os.Stat(to); err == nil {
		return fmt.Errorf("目标路径已存在")
	}
	if err := os.MkdirAll(filepath.Dir(to), 0755); err != nil {
		return err
	}
	if err := os.Rename(from, to); err != nil {
		return fmt.Errorf("重命名路径失败: %w", err)
	}
	return a.refreshWorkspaceAfterFileOperation()
}

func (a *App) MoveWorkspacePaths(paths []string, targetDir string) error {
	return a.moveWorkspacePaths(paths, targetDir)
}

func (a *App) moveWorkspacePaths(paths []string, targetDir string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.workspace == "" || a.snapEng == nil {
		return fmt.Errorf("未选择工作区")
	}
	targetDir, err := a.workspaceRelativePath(targetDir, true)
	if err != nil {
		return err
	}
	for _, sourcePath := range paths {
		sourcePath, err = a.workspaceRelativePath(sourcePath, false)
		if err != nil {
			return err
		}
		destinationPath := filepath.ToSlash(filepath.Join(targetDir, filepath.Base(sourcePath)))
		if sourcePath == destinationPath {
			continue
		}
		if strings.HasPrefix(destinationPath+"/", sourcePath+"/") {
			return fmt.Errorf("不能移动到自身或其子目录")
		}
		if a.isRemote {
			destination := path.Join(a.remotePath, destinationPath)
			if _, err := a.remoteSFTP.Stat(destination); err == nil {
				return fmt.Errorf("目标路径已存在: %s", destinationPath)
			}
			if err := a.remoteSFTP.Rename(path.Join(a.remotePath, sourcePath), destination); err != nil {
				return fmt.Errorf("移动远程文件失败: %w", err)
			}
			continue
		}
		destination := filepath.Join(a.workspace, filepath.FromSlash(destinationPath))
		if _, err := os.Stat(destination); err == nil {
			return fmt.Errorf("目标路径已存在: %s", destinationPath)
		}
		if err := os.Rename(filepath.Join(a.workspace, filepath.FromSlash(sourcePath)), destination); err != nil {
			return fmt.Errorf("移动文件失败: %w", err)
		}
	}
	return a.refreshWorkspaceAfterFileOperation()
}

func (a *App) UploadWorkspacePaths(sourcePaths []string, targetDir string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.workspace == "" || a.snapEng == nil {
		return fmt.Errorf("未选择工作区")
	}
	targetDir, err := a.workspaceRelativePath(targetDir, true)
	if err != nil {
		return err
	}
	for _, sourcePath := range sourcePaths {
		info, err := os.Stat(sourcePath)
		if err != nil {
			return fmt.Errorf("读取拖入路径失败: %w", err)
		}
		if !a.isRemote {
			rel, err := filepath.Rel(a.workspace, sourcePath)
			if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
				return fmt.Errorf("不能上传工作区内的文件，请直接拖动以移动")
			}
		}
		destinationPath := filepath.ToSlash(filepath.Join(targetDir, filepath.Base(sourcePath)))
		if a.isRemote {
			if _, err := a.remoteSFTP.Stat(path.Join(a.remotePath, destinationPath)); err == nil {
				return fmt.Errorf("目标路径已存在: %s", destinationPath)
			}
			if err := uploadRemoteWorkspacePath(a.remoteSFTP, sourcePath, path.Join(a.remotePath, destinationPath), info); err != nil {
				return err
			}
			continue
		}
		destination := filepath.Join(a.workspace, filepath.FromSlash(destinationPath))
		if _, err := os.Stat(destination); err == nil {
			return fmt.Errorf("目标路径已存在: %s", destinationPath)
		}
		if err := copyWorkspacePath(sourcePath, destination); err != nil {
			return fmt.Errorf("上传文件失败: %w", err)
		}
	}
	return a.refreshWorkspaceAfterFileOperation()
}

func uploadRemoteWorkspacePath(client *sftp.Client, source, destination string, info os.FileInfo) error {
	if !info.IsDir() {
		return copyLocalFileToRemote(client, source, destination)
	}
	return filepath.Walk(source, func(current string, currentInfo os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, current)
		if err != nil {
			return err
		}
		target := path.Join(destination, filepath.ToSlash(rel))
		if currentInfo.IsDir() {
			return client.MkdirAll(target)
		}
		return copyLocalFileToRemote(client, current, target)
	})
}

func copyLocalFileToRemote(client *sftp.Client, source, destination string) error {
	if err := client.MkdirAll(path.Dir(destination)); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := client.Create(destination)
	if err != nil {
		return err
	}
	defer output.Close()
	_, err = io.Copy(output, input)
	return err
}

// CopyWorkspacePaths copies multiple local paths into targetDir. Existing targets are rejected.
func (a *App) CopyWorkspacePaths(paths []string, targetDir string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.workspace == "" || a.snapEng == nil {
		return fmt.Errorf("未选择工作区")
	}
	targetDir, err := a.workspaceRelativePath(targetDir, true)
	if err != nil {
		return err
	}
	for _, sourcePath := range paths {
		sourcePath, err = a.workspaceRelativePath(sourcePath, false)
		if err != nil {
			return err
		}
		destinationPath := filepath.ToSlash(filepath.Join(targetDir, filepath.Base(sourcePath)))
		if sourcePath == destinationPath || strings.HasPrefix(destinationPath+"/", sourcePath+"/") {
			return fmt.Errorf("不能复制到自身或其子目录")
		}
		if a.isRemote {
			destination := path.Join(a.remotePath, destinationPath)
			if _, err := a.remoteSFTP.Stat(destination); err == nil {
				return fmt.Errorf("目标路径已存在: %s", destinationPath)
			}
			if err := copyRemoteWorkspacePath(a.remoteSFTP, path.Join(a.remotePath, sourcePath), destination); err != nil {
				return err
			}
			continue
		}
		destination := filepath.Join(a.workspace, destinationPath)
		if _, err := os.Stat(destination); err == nil {
			return fmt.Errorf("目标路径已存在: %s", destinationPath)
		}
		if err := copyWorkspacePath(filepath.Join(a.workspace, sourcePath), destination); err != nil {
			return err
		}
	}
	return a.refreshWorkspaceAfterFileOperation()
}

func copyRemoteWorkspacePath(client *sftp.Client, source, destination string) error {
	info, err := client.Stat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return copyRemoteWorkspaceFile(client, source, destination)
	}
	walker := client.Walk(source)
	for walker.Step() {
		if walker.Err() != nil {
			return walker.Err()
		}
		currentInfo := walker.Stat()
		if currentInfo == nil {
			continue
		}
		rel, err := filepath.Rel(filepath.FromSlash(source), filepath.FromSlash(walker.Path()))
		if err != nil {
			return err
		}
		target := path.Join(destination, filepath.ToSlash(rel))
		if currentInfo.IsDir() {
			if err := client.MkdirAll(target); err != nil {
				return err
			}
			continue
		}
		if err := copyRemoteWorkspaceFile(client, walker.Path(), target); err != nil {
			return err
		}
	}
	return nil
}

func copyRemoteWorkspaceFile(client *sftp.Client, source, destination string) error {
	if err := client.MkdirAll(path.Dir(destination)); err != nil {
		return err
	}
	input, err := client.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := client.Create(destination)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func copyWorkspacePath(source, destination string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		data, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
			return err
		}
		return os.WriteFile(destination, data, info.Mode())
	}
	return filepath.Walk(source, func(current string, currentInfo os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, current)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		if currentInfo.IsDir() {
			return os.MkdirAll(target, currentInfo.Mode())
		}
		data, err := os.ReadFile(current)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		return os.WriteFile(target, data, currentInfo.Mode())
	})
}

// ─── Terminal ────────────────────────────────────────

func (a *App) CreateTerminal() (string, error) {
	var id string
	var err error
	if a.isRemote {
		id, err = a.termMgr.CreateSSH(a.remoteSSHCfg)
	} else {
		id, err = a.termMgr.Create(a.workspace)
	}
	if err != nil {
		return "", err
	}
	sess, _ := a.termMgr.Get(id)
	go a.readTerminalOutput(id, sess)

	if a.isRemote && a.remotePath != "" {
		sess.Write([]byte("cd " + shellQuote(a.remotePath) + "\n"))
	}
	return id, nil
}

func (a *App) GetTerminalSnapshots() ([]config.TerminalSnapshot, error) {
	if a.cfgStore == nil {
		return nil, nil
	}
	items, err := a.cfgStore.LoadTerminalSnapshots()
	if err != nil {
		return nil, err
	}
	workspace := a.workspace
	migrated := false
	for i := range items {
		if items[i].Workspace != "" {
			continue
		}
		if items[i].Type == "ssh" || items[i].SSHName != "" {
			// Legacy SSH snapshot: use saved cwd if present, otherwise keep unresolved.
			items[i].Workspace = items[i].CWD
		} else {
			// Legacy local snapshot: cwd is the local workspace path.
			items[i].Workspace = items[i].CWD
		}
		if items[i].Workspace != "" {
			migrated = true
		}
	}
	if migrated {
		_ = a.cfgStore.SaveTerminalSnapshots(items)
	}
	filtered := make([]config.TerminalSnapshot, 0, len(items))
	for _, item := range items {
		if item.Workspace == workspace {
			item.Restored = true
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func (a *App) SaveTerminalSnapshots(items []config.TerminalSnapshot) error {
	if a.cfgStore == nil {
		return nil
	}
	return a.cfgStore.SaveTerminalSnapshots(items)
}

func (a *App) ReconnectTerminal(snap config.TerminalSnapshot) (string, error) {
	if snap.Type == "ssh" || snap.SSHName != "" {
		if a.cfgStore == nil {
			return "", fmt.Errorf("配置存储不可用")
		}
		cfgs, err := a.cfgStore.LoadSSHConfigs()
		if err != nil {
			return "", err
		}
		for _, c := range cfgs {
			if c.Name == snap.SSHName || c.Name == snap.Title {
				id, err := a.termMgr.CreateSSH(terminal.SSHConfig{
					Name: c.Name, Host: c.Host, Port: c.Port,
					User: c.User, Password: c.Password, KeyPath: c.KeyPath,
				})
				if err != nil {
					return "", err
				}
				sess, _ := a.termMgr.Get(id)
				go a.readTerminalOutput(id, sess)
				return id, nil
			}
		}
		return "", fmt.Errorf("SSH 配置不存在: %s", snap.SSHName)
	}
	id, err := a.termMgr.Create(snap.CWD)
	if err != nil {
		return "", err
	}
	sess, _ := a.termMgr.Get(id)
	go a.readTerminalOutput(id, sess)
	return id, nil
}

func (a *App) WriteToTerminal(tabId, data string) error {
	sess, err := a.termMgr.Get(tabId)
	if err != nil {
		return err
	}
	_, err = sess.Write([]byte(data))
	return err
}

func (a *App) ResizeTerminal(tabId string, cols, rows int) error {
	sess, err := a.termMgr.Get(tabId)
	if err != nil {
		return err
	}
	return sess.Resize(uint16(rows), uint16(cols))
}

func (a *App) CloseTerminal(tabId string) error {
	return a.termMgr.Close(tabId)
}

// ─── SSH ─────────────────────────────────────────────

func (a *App) CreateSSHTerminal(cfg SSHConfig) (string, error) {
	tCfg := terminal.SSHConfig{
		Name: cfg.Name, Host: cfg.Host, Port: cfg.Port,
		User: cfg.User, Password: cfg.Password, KeyPath: cfg.KeyPath,
	}
	id, err := a.termMgr.CreateSSH(tCfg)
	if err != nil {
		return "", err
	}
	sess, _ := a.termMgr.Get(id)
	go a.readTerminalOutput(id, sess)
	return id, nil
}

func (a *App) GetSSHConfigs() ([]config.SSHConfig, error) {
	if a.cfgStore == nil {
		return nil, nil
	}
	return a.cfgStore.LoadSSHConfigs()
}

func (a *App) SaveSSHConfig(cfg config.SSHConfig) error {
	if a.cfgStore == nil {
		return fmt.Errorf("配置存储不可用")
	}
	return a.cfgStore.SaveSSHConfig(cfg)
}

func (a *App) RemoveSSHConfig(name string) error {
	if a.cfgStore == nil {
		return nil
	}
	return a.cfgStore.RemoveSSHConfig(name)
}

// ─── AI Configs ──────────────────────────────────────

type AIToolPaths struct {
	ClaudeCode string `json:"claudeCode"`
	Codex      string `json:"codex"`
	OpenCode   string `json:"openCode"`
}

func (a *App) GetAIConfigGroups() ([]config.AIConfigGroup, error) {
	if a.cfgStore == nil {
		return nil, nil
	}
	return a.cfgStore.LoadAIConfigGroups()
}

func (a *App) SaveAIConfigGroups(groups []config.AIConfigGroup) error {
	if a.cfgStore == nil {
		return fmt.Errorf("配置存储不可用")
	}
	return a.cfgStore.SaveAIConfigGroups(groups)
}

// ─── Appearance ──────────────────────────────────────

// maxBackgroundImageSize caps the image we are willing to inline as a data URL.
const maxBackgroundImageSize = 12 * 1024 * 1024

// backgroundImageMIME maps supported image extensions to their MIME type.
// Only these extensions are accepted as background images.
var backgroundImageMIME = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".bmp":  "image/bmp",
}

func (a *App) GetAppearance() (config.Appearance, error) {
	if a.cfgStore == nil {
		return config.DefaultAppearance(), nil
	}
	return a.cfgStore.LoadAppearance()
}

func (a *App) SaveAppearance(ap config.Appearance) error {
	if a.cfgStore == nil {
		return fmt.Errorf("配置存储不可用")
	}
	return a.cfgStore.SaveAppearance(ap)
}

// SelectBackgroundImage opens a picker and returns the chosen absolute path.
// An empty return means the user cancelled.
func (a *App) SelectBackgroundImage() (string, error) {
	picked, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择背景图片",
		Filters: []runtime.FileFilter{{
			DisplayName: "图片 (*.png;*.jpg;*.jpeg;*.gif;*.webp;*.bmp)",
			Pattern:     "*.png;*.jpg;*.jpeg;*.gif;*.webp;*.bmp",
		}},
	})
	if err != nil {
		return "", fmt.Errorf("选择图片失败: %w", err)
	}
	if picked == "" {
		return "", nil
	}
	if _, err := a.GetBackgroundImageData(picked); err != nil {
		return "", err
	}
	return picked, nil
}

// GetBackgroundImageData reads a local image and returns it as a data URL for
// the webview, which cannot load arbitrary file:// paths itself. Only the
// extensions in backgroundImageMIME are served, and only up to
// maxBackgroundImageSize.
func (a *App) GetBackgroundImageData(imgPath string) (string, error) {
	if imgPath == "" {
		return "", nil
	}
	mimeType, ok := backgroundImageMIME[strings.ToLower(filepath.Ext(imgPath))]
	if !ok {
		return "", fmt.Errorf("不支持的图片格式")
	}
	info, err := os.Stat(imgPath)
	if err != nil {
		return "", fmt.Errorf("读取图片失败: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("不支持的图片格式")
	}
	if info.Size() > maxBackgroundImageSize {
		return "", fmt.Errorf("图片过大，请选择小于 12MB 的图片")
	}
	data, err := os.ReadFile(imgPath)
	if err != nil {
		return "", fmt.Errorf("读取图片失败: %w", err)
	}
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

func (a *App) DetectAIToolConfigPaths() AIToolPaths {
	home := ""
	if a.isRemote && a.remoteClient != nil {
		home = a.remoteHomeDir()
	} else {
		home, _ = os.UserHomeDir()
	}
	paths := AIToolPaths{}
	if home == "" {
		return paths
	}
	if a.isRemote {
		paths.ClaudeCode = path.Join(home, ".claude", "settings.json")
		paths.Codex = path.Join(home, ".codex", "config.toml")
		paths.OpenCode = path.Join(home, ".config", "opencode", "opencode.json")
	} else {
		paths.ClaudeCode = filepath.Join(home, ".claude", "settings.json")
		paths.Codex = filepath.Join(home, ".codex", "config.toml")
		paths.OpenCode = filepath.Join(home, ".config", "opencode", "opencode.json")
	}
	return paths
}

func (a *App) remoteHomeDir() string {
	if a.remoteClient == nil {
		return ""
	}
	sess, err := a.remoteClient.NewSession()
	if err != nil {
		return ""
	}
	defer sess.Close()
	out, err := sess.Output("printf %s \"$HOME\"")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (a *App) ApplyAIConfigGroup(group config.AIConfigGroup, target string) error {
	paths := a.DetectAIToolConfigPaths()
	if target == "" || target == "all" || target == "claudeCode" {
		if err := a.writeClaudeCodeConfig(paths.ClaudeCode, group); err != nil {
			return err
		}
	}
	if target == "" || target == "all" || target == "codex" {
		if err := a.writeCodexConfig(paths.Codex, group); err != nil {
			return err
		}
	}
	if target == "" || target == "all" || target == "openCode" {
		if err := a.writeSimpleModelListConfig(paths.OpenCode, group); err != nil {
			return err
		}
	}
	return nil
}

func ensureV1BaseURL(raw string) string {
	base := strings.TrimSpace(raw)
	base = strings.TrimRight(base, "/")
	if base == "" {
		return ""
	}
	if strings.HasSuffix(base, "/v1") {
		return base
	}
	return base + "/v1"
}

func (a *App) writeClaudeCodeConfig(path string, group config.AIConfigGroup) error {
	if group.ClaudeCode.OpusIndex < 0 || group.ClaudeCode.OpusIndex >= len(group.Models) {
		return fmt.Errorf("Claude Code Opus 模型索引无效")
	}
	if group.ClaudeCode.SonnetIndex < 0 || group.ClaudeCode.SonnetIndex >= len(group.Models) {
		return fmt.Errorf("Claude Code Sonnet 模型索引无效")
	}
	if group.ClaudeCode.HaikuIndex < 0 || group.ClaudeCode.HaikuIndex >= len(group.Models) {
		return fmt.Errorf("Claude Code Haiku 模型索引无效")
	}
	cfg := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &cfg)
	}
	env, _ := cfg["env"].(map[string]any)
	if env == nil {
		env = map[string]any{}
	}
	env["ANTHROPIC_AUTH_TOKEN"] = group.APIKey
	env["ANTHROPIC_BASE_URL"] = group.BaseURL
	env["ANTHROPIC_DEFAULT_OPUS_MODEL"] = group.Models[group.ClaudeCode.OpusIndex]
	env["ANTHROPIC_DEFAULT_SONNET_MODEL"] = group.Models[group.ClaudeCode.SonnetIndex]
	env["ANTHROPIC_DEFAULT_HAIKU_MODEL"] = group.Models[group.ClaudeCode.HaikuIndex]
	env["ANTHROPIC_SMALL_FAST_MODEL"] = group.Models[group.ClaudeCode.HaikuIndex]
	cfg["env"] = env
	if _, ok := cfg["model"]; !ok {
		cfg["model"] = "opus"
	}
	return a.writeJSONFileForTarget(path, cfg)
}

func (a *App) writeCodexConfig(path string, group config.AIConfigGroup) error {
	if len(group.Models) == 0 {
		return fmt.Errorf("Codex 模型池不能为空")
	}
	content := fmt.Sprintf(`# ==========================================
# Generated by aimuxterm AI config manager
# ==========================================
model = %q
model_provider = %q
model_reasoning_effort = %q
model_context_window = 128000
model_auto_compact_token_limit = 100000
disable_response_storage = true
sandbox_mode = %q

[model_providers.azure]
name = %q
base_url = %q
env_key = %q
wire_api = %q

[features]
plan_tool = true
apply_patch_freeform = true
view_image_tool = true

[sandbox_workspace_write]
network_access = true
`, group.Models[0], "azure", "high", "workspace-write", "Azure OpenAI", ensureV1BaseURL(group.BaseURL), "AZURE_OPENAI_API_KEY", "responses")
	if !a.isRemote || a.remoteSFTP == nil {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(content), 0644)
	}
	if err := a.remoteSFTP.MkdirAll(pathpkgDir(path)); err != nil {
		return err
	}
	f, err := a.remoteSFTP.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write([]byte(content))
	return err
}

func (a *App) writeSimpleModelListConfig(path string, group config.AIConfigGroup) error {
	cfg := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &cfg)
	}
	if strings.Contains(strings.ToLower(path), "opencode") {
		cfg = map[string]any{
			"provider": map[string]any{
				"openai": map[string]any{
					"options": map[string]any{
						"baseURL": ensureV1BaseURL(group.BaseURL),
						"apiKey":  group.APIKey,
					},
					"models": buildOpenCodeModels(group.Models),
				},
			},
			"$schema": "https://opencode.ai/config.json",
		}
		return a.writeJSONFileForTarget(path, cfg)
	}
	cfg["apiKey"] = group.APIKey
	cfg["baseURL"] = ensureV1BaseURL(group.BaseURL)
	cfg["models"] = group.Models
	if len(group.Models) > 0 {
		cfg["defaultModel"] = group.Models[0]
	}
	return a.writeJSONFileForTarget(path, cfg)
}

func buildOpenCodeModels(models []string) map[string]any {
	out := map[string]any{}
	for _, m := range models {
		if strings.TrimSpace(m) == "" {
			continue
		}
		out[m] = map[string]any{
			"name": m,
		}
	}
	return out
}

func writeJSONFile(path string, cfg map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func (a *App) writeJSONFileForTarget(path string, cfg map[string]any) error {
	if !a.isRemote || a.remoteSFTP == nil {
		return writeJSONFile(path, cfg)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := a.remoteSFTP.MkdirAll(pathpkgDir(path)); err != nil {
		return err
	}
	f, err := a.remoteSFTP.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

func pathpkgDir(p string) string {
	if idx := strings.LastIndex(p, "/"); idx >= 0 {
		return p[:idx]
	}
	return "."
}

// ─── Startup Commands ──────────────────────────────────

func (a *App) GetStartupCommands() ([]config.StartupCommand, error) {
	if a.cfgStore == nil {
		return nil, nil
	}
	return a.cfgStore.LoadStartupCommands()
}

func (a *App) SaveStartupCommands(cmds []config.StartupCommand) error {
	if a.cfgStore == nil {
		return fmt.Errorf("配置存储不可用")
	}
	return a.cfgStore.SaveStartupCommands(cmds)
}

// ─── Project Run Commands ──────────────────────────────

func (a *App) GetProjectRunCommands() ([]config.ProjectRunCommand, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfgStore == nil || a.workspace == "" {
		return nil, nil
	}
	return a.cfgStore.LoadProjectRunCommands(a.workspace)
}

func (a *App) SaveProjectRunCommands(cmds []config.ProjectRunCommand) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfgStore == nil {
		return fmt.Errorf("配置存储不可用")
	}
	if a.workspace == "" {
		return fmt.Errorf("未打开工作区")
	}
	return a.cfgStore.SaveProjectRunCommands(a.workspace, cmds)
}

const (
	terminalReadBufferSize = 32 * 1024
	terminalEmitBufferSize = 64 * 1024
	terminalEmitInterval   = 8 * time.Millisecond
)

type terminalReadResult struct {
	data []byte
	err  error
}

// ─── ACP ─────────────────────────────────────────────

type AcpSessionInfo struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Mode   string `json:"mode"`
	Agent  string `json:"agent"`
	Cwd    string `json:"cwd"`
	Status string `json:"status"`
}

func (a *App) GetAcpAgents() ([]config.AcpAgentConfig, error) {
	if a.cfgStore == nil {
		return nil, nil
	}
	return a.cfgStore.LoadAcpAgents()
}

func (a *App) SaveAcpAgents(items []config.AcpAgentConfig) error {
	if a.cfgStore == nil {
		return fmt.Errorf("配置存储不可用")
	}
	return a.cfgStore.SaveAcpAgents(items)
}

func (a *App) CreateAcpSession(agentID string) (AcpSessionInfo, error) {
	var empty AcpSessionInfo
	if a.acpMgr == nil {
		return empty, fmt.Errorf("ACP 未初始化")
	}
	agents, err := a.GetAcpAgents()
	if err != nil {
		return empty, err
	}
	if len(agents) == 0 {
		return empty, fmt.Errorf("请先在设置中配置 ACP Agent 命令")
	}
	var cfg *config.AcpAgentConfig
	if agentID != "" {
		for i := range agents {
			if agents[i].ID == agentID || agents[i].Name == agentID {
				cfg = &agents[i]
				break
			}
		}
		if cfg == nil {
			return empty, fmt.Errorf("找不到 ACP Agent: %s", agentID)
		}
	} else {
		for i := range agents {
			if agents[i].IsDefault {
				cfg = &agents[i]
				break
			}
		}
		if cfg == nil {
			cfg = &agents[0]
		}
	}
	if strings.TrimSpace(cfg.Command) == "" && strings.TrimSpace(cfg.RemoteCommand) == "" {
		return empty, fmt.Errorf("Agent 命令为空")
	}

	a.mu.Lock()
	isRemote := a.isRemote
	cwd := a.workspace
	remotePath := a.remotePath
	sshCfg := a.remoteSSHCfg
	a.mu.Unlock()

	opts := acp.CreateOptions{
		Launch: acp.LaunchFromConfig(*cfg),
		Title:  cfg.Name,
	}
	if isRemote {
		if sshCfg.Host == "" {
			return empty, fmt.Errorf("远程工作区未连接")
		}
		opts.Mode = "remote"
		opts.Cwd = remotePath
		opts.SSH = &acp.SSHParams{
			Host:     sshCfg.Host,
			Port:     sshCfg.Port,
			User:     sshCfg.User,
			Password: sshCfg.Password,
			KeyPath:  sshCfg.KeyPath,
		}
	} else {
		opts.Mode = "local"
		opts.Cwd = cwd
	}
	sess, err := a.acpMgr.Create(opts)
	if err != nil {
		return empty, err
	}
	return AcpSessionInfo{
		ID:     sess.ID,
		Title:  sess.Title,
		Mode:   sess.Mode,
		Agent:  sess.Agent,
		Cwd:    sess.Cwd,
		Status: sess.Status,
	}, nil
}

func (a *App) CloseAcpSession(id string) error {
	if a.acpMgr == nil {
		return nil
	}
	return a.acpMgr.Close(id)
}

func (a *App) SendAcpPrompt(id, text string) error {
	if a.acpMgr == nil {
		return fmt.Errorf("ACP 未初始化")
	}
	return a.acpMgr.Prompt(id, text)
}

func (a *App) RespondAcpPermission(id, requestID, optionID string) error {
	if a.acpMgr == nil {
		return fmt.Errorf("ACP not initialized")
	}
	if strings.TrimSpace(optionID) == "" {
		optionID = "reject"
	}
	return a.acpMgr.RespondPermission(id, requestID, optionID)
}

func (a *App) CancelAcpPrompt(id string) error {
	if a.acpMgr == nil {
		return fmt.Errorf("ACP not initialized")
	}
	return a.acpMgr.Cancel(id)
}

func (a *App) SetAcpSessionMode(id, modeID string) error {
	if a.acpMgr == nil {
		return fmt.Errorf("ACP not initialized")
	}
	return a.acpMgr.SetMode(id, modeID)
}

func (a *App) readTerminalOutput(id string, sess *terminal.Session) {
	results := make(chan terminalReadResult, 4)
	go func() {
		buf := make([]byte, terminalReadBufferSize)
		for {
			n, err := sess.Read(buf)
			if n > 0 {
				data := append([]byte(nil), buf[:n]...)
				results <- terminalReadResult{data: data}
			}
			if err != nil {
				results <- terminalReadResult{err: err}
				return
			}
		}
	}()

	ticker := time.NewTicker(terminalEmitInterval)
	defer ticker.Stop()
	var pending strings.Builder
	flush := func() {
		if pending.Len() == 0 {
			return
		}
		runtime.EventsEmit(a.ctx, "terminal-output:"+id, pending.String())
		pending.Reset()
	}

	for {
		select {
		case result := <-results:
			if len(result.data) > 0 {
				pending.Write(result.data)
				if pending.Len() >= terminalEmitBufferSize {
					flush()
				}
			}
			if result.err != nil {
				flush()
				_ = a.termMgr.Close(id)
				runtime.EventsEmit(a.ctx, "terminal-output:"+id, "\r\n[终端已关闭]")
				return
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (a *App) refreshScan() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.refreshScanLocked()
}

func (a *App) refreshScanLocked() {
	if a.isRemote {
		entries, err := a.listRemoteFiles(a.remoteSFTP, a.remotePath)
		if err != nil {
			return
		}
		a.scannedRemoteEntries = entries
		a.scannedFiles = entriesToPaths(entries)
		a.cachedChanges = nil
		a.changesCached = false
		return
	}
	result, err := scanner.Scan(a.workspace)
	if err != nil {
		return
	}
	a.scannedFiles = result.Files
	a.scannedOtherFiles = result.OtherFiles
	a.scannedDirectories = result.Directories
	a.cachedChanges = nil
	a.changesCached = false
}

func (a *App) emitChanges() {
	a.cachedChanges = nil
	a.changesCached = false
	changes := a.workspaceChangesLocked()
	runtime.EventsEmit(a.ctx, "file-changes", changes)
}
