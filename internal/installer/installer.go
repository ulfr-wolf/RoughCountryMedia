// Package installer sets RCM up as a boot-time service on a Linux machine:
// `rcm install` (guided, re-run to upgrade) and `rcm uninstall`.
//
// Every filesystem path goes through Env.p and every system command through
// Env.Run, so the whole flow can be tested on any machine with a fake root.
package installer

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	BinPath    = "/usr/local/bin/rcm"
	UnitPath   = "/etc/systemd/system/rcm.service"
	ConfPath   = "/etc/rcm/rcm.conf"
	SysctlPath = "/etc/sysctl.d/90-rcm-inotify.conf"
	DataDir    = "/var/lib/rcm"
	RepoURL    = "https://github.com/ulfr-wolf/RoughCountryMedia"
)

// Env is everything the installer touches on the machine.
type Env struct {
	Root       string // "/" for real installs; a temp folder in tests
	In         *bufio.Reader
	Out        io.Writer
	Run        func(name string, args ...string) (string, error)
	LookPath   func(name string) (string, error)
	LookupUser func(name string) (*user.User, error)
	PortFree   func(port int) bool
	BaseURL    func(port int) string
	Hostname   func() (string, error)
	Getenv     func(string) string
	Sleep      func(time.Duration)
	Executable string
	Version    string
	IsRoot     bool
	HasSystemd bool
	HTTP       *http.Client
}

func DefaultEnv(version string) *Env {
	exe, _ := os.Executable()
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	_, sdErr := os.Stat("/run/systemd/system")
	return &Env{
		Root: "/",
		In:   bufio.NewReader(os.Stdin),
		Out:  os.Stdout,
		Run: func(name string, args ...string) (string, error) {
			out, err := exec.Command(name, args...).CombinedOutput()
			return string(out), err
		},
		LookPath:   exec.LookPath,
		LookupUser: user.Lookup,
		PortFree: func(port int) bool {
			l, err := net.Listen("tcp", ":"+strconv.Itoa(port))
			if err != nil {
				return false
			}
			l.Close()
			return true
		},
		BaseURL:    func(port int) string { return fmt.Sprintf("http://127.0.0.1:%d", port) },
		Hostname:   os.Hostname,
		Getenv:     os.Getenv,
		Sleep:      time.Sleep,
		Executable: exe,
		Version:    version,
		IsRoot:     os.Geteuid() == 0,
		HasSystemd: sdErr == nil,
		HTTP:       &http.Client{Timeout: 10 * time.Second},
	}
}

// p maps an absolute path on the target system into Env.Root.
func (e *Env) p(abs string) string { return filepath.Join(e.Root, abs) }

// ---- configuration ----

// Config is saved in /etc/rcm/rcm.conf so re-running the installer can upgrade without questions.
type Config struct {
	User      string
	Port      int
	ImportDir string
	Media     []string
}

func (c Config) marshal() string {
	var b strings.Builder
	b.WriteString("# Rough Country Media install settings (written by `rcm install`)\n")
	fmt.Fprintf(&b, "USER=%s\nPORT=%d\nIMPORT=%s\n", c.User, c.Port, c.ImportDir)
	for _, m := range c.Media {
		fmt.Fprintf(&b, "MEDIA=%s\n", m)
	}
	return b.String()
}

func parseConfig(s string) Config {
	var c Config
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || strings.HasPrefix(k, "#") {
			continue
		}
		switch k {
		case "USER":
			c.User = v
		case "PORT":
			c.Port, _ = strconv.Atoi(v)
		case "IMPORT":
			c.ImportDir = v
		case "MEDIA":
			c.Media = append(c.Media, v)
		}
	}
	return c
}

func (e *Env) loadConfig() (Config, bool) {
	b, err := os.ReadFile(e.p(ConfPath))
	if err != nil {
		return Config{}, false
	}
	return parseConfig(string(b)), true
}

// ---- service file ----

// sdQuote quotes a value for a systemd ExecStart line (spaces, quotes, % and $ are special there).
func sdQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$").Replace(s) + `"`
}

func unitFile(c Config, group string) string {
	return fmt.Sprintf(`# Written by "rcm install". Re-run it to change settings, or "rcm uninstall" to remove.
[Unit]
Description=Rough Country Media server
Documentation=%s
After=network-online.target local-fs.target remote-fs.target
Wants=network-online.target

[Service]
User=%s
Group=%s
ExecStart=%s -addr :%d -data %s -import %s
Restart=on-failure
RestartSec=5
# Be a good neighbor on an older machine.
Nice=5
IOSchedulingClass=best-effort
IOSchedulingPriority=6
NoNewPrivileges=true
ProtectSystem=full
PrivateTmp=true

[Install]
WantedBy=multi-user.target
`, RepoURL, c.User, group, BinPath, c.Port, sdQuote(DataDir), sdQuote(c.ImportDir))
}

// ---- prompts ----

func (e *Env) say(format string, a ...any) { fmt.Fprintf(e.Out, format+"\n", a...) }
func (e *Env) step(format string, a ...any) {
	fmt.Fprintf(e.Out, "→ "+format+"\n", a...)
}
func (e *Env) ok(format string, a ...any)   { fmt.Fprintf(e.Out, "  ✓ "+format+"\n", a...) }
func (e *Env) warn(format string, a ...any) { fmt.Fprintf(e.Out, "  ! "+format+"\n", a...) }

func (e *Env) readLine() (string, bool) {
	line, err := e.In.ReadString('\n')
	if err != nil && line == "" {
		return "", false
	}
	return strings.TrimSpace(line), true
}

func (e *Env) ask(q, def string) string {
	if def != "" {
		fmt.Fprintf(e.Out, "%s [%s]: ", q, def)
	} else {
		fmt.Fprintf(e.Out, "%s: ", q)
	}
	line, ok := e.readLine()
	if !ok {
		fmt.Fprintln(e.Out)
	}
	if line == "" {
		return def
	}
	return line
}

func (e *Env) confirm(q string, def bool) bool {
	hint := "Y/n"
	if !def {
		hint = "y/N"
	}
	for {
		fmt.Fprintf(e.Out, "%s [%s]: ", q, hint)
		line, ok := e.readLine()
		if !ok || line == "" {
			if !ok {
				fmt.Fprintln(e.Out)
			}
			return def
		}
		switch strings.ToLower(line) {
		case "y", "yes":
			return true
		case "n", "no":
			return false
		}
	}
}

// ---- entry point ----

type Options struct {
	Yes         bool
	Reconfigure bool
	Purge       bool
	Port        int
	User        string
	ImportDir   string
	Media       []string
}

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

var errCancelled = errors.New("cancelled; nothing was changed")

// Main runs `rcm install ...` or `rcm uninstall ...` and returns the exit code.
func Main(args []string, version string) int {
	e := DefaultEnv(version)
	var o Options
	var media multi
	fs := flag.NewFlagSet("rcm "+args[0], flag.ContinueOnError)
	fs.SetOutput(e.Out)
	fs.BoolVar(&o.Yes, "yes", false, "accept the defaults without asking")
	switch args[0] {
	case "install":
		fs.BoolVar(&o.Reconfigure, "reconfigure", false, "ask the setup questions again on an existing install")
		fs.IntVar(&o.Port, "port", 0, "port for the web UI (default 8080)")
		fs.StringVar(&o.User, "user", "", "account RCM runs as (default: the account that ran sudo)")
		fs.StringVar(&o.ImportDir, "import", "", "folder where drag-and-drop imports are saved")
		fs.Var(&media, "media", "media folder to add (repeatable)")
	case "uninstall":
		fs.BoolVar(&o.Purge, "purge", false, "also delete RCM's database and cache (never your media)")
	}
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	o.Media = media
	var err error
	if args[0] == "install" {
		err = e.Install(o)
	} else {
		err = e.Uninstall(o)
	}
	if err != nil {
		fmt.Fprintf(e.Out, "\n✗ %v\n", err)
		return 1
	}
	return 0
}

// ---- install ----

func (e *Env) preflight() error {
	if runtime.GOOS != "linux" && e.Root == "/" {
		return fmt.Errorf("`rcm install` sets RCM up on Linux; this is %s. Copy the Linux build to the Mac Mini and run it there (see deploy.sh)", runtime.GOOS)
	}
	if !e.IsRoot {
		return errors.New("the installer needs administrator rights: run it with sudo, e.g. `sudo ./rcm-linux-amd64 install`")
	}
	return nil
}

func (e *Env) Install(o Options) error {
	if err := e.preflight(); err != nil {
		return err
	}
	e.say("\nRough Country Media installer (%s)\n", e.Version)
	old, existing := e.loadConfig()
	if existing && !o.Reconfigure {
		return e.upgrade(old)
	}
	c, err := e.configure(o, old, existing)
	if err != nil {
		return err
	}
	u, err := e.LookupUser(c.User)
	if err != nil {
		return fmt.Errorf("user %q not found", c.User)
	}
	group := u.Username
	if g, err := user.LookupGroupId(u.Gid); err == nil {
		group = g.Name
	}

	e.say("\nReady to install:")
	e.say("  Program:        %s", BinPath)
	e.say("  Runs as:        %s (starts automatically at boot)", c.User)
	e.say("  Web address:    port %d", c.Port)
	e.say("  Imports go to:  %s", c.ImportDir)
	if len(c.Media) == 0 {
		e.say("  Media folders:  none yet (add them later in Settings)")
	}
	for i, m := range c.Media {
		label := "  Media folders:  "
		if i > 0 {
			label = "                  "
		}
		e.say("%s%s", label, m)
	}
	e.say("  Database/cache: %s", DataDir)
	if !o.Yes && !e.confirm("\nInstall now?", true) {
		return errCancelled
	}
	e.say("")

	e.ensurePackages(o.Yes)
	if err := e.installBinary(); err != nil {
		return err
	}
	if err := e.makeDirs(c, u); err != nil {
		return err
	}
	if err := e.writeFile(ConfPath, c.marshal(), 0o644); err != nil {
		return err
	}
	e.raiseWatchLimit()
	if err := e.writeFile(UnitPath, unitFile(c, group), 0o644); err != nil {
		return err
	}
	e.ok("service file %s", UnitPath)
	e.openFirewall(c.Port, o.Yes)
	if err := e.startService(); err != nil {
		return err
	}
	if !e.HasSystemd {
		return nil
	}
	if err := e.waitHealthy(c.Port); err != nil {
		return err
	}
	e.addLibraries(c)
	e.finish(c.Port)
	return nil
}

// configure asks the setup questions (or takes them from flags with --yes).
func (e *Env) configure(o Options, old Config, existing bool) (Config, error) {
	c := Config{User: o.User, Port: o.Port, ImportDir: o.ImportDir, Media: o.Media}

	// Who runs the service: the person who installed it owns the media, which
	// avoids permission problems with external drives.
	if c.User == "root" {
		return c, errors.New("RCM shouldn't run as root; pick a normal user account with --user NAME")
	}
	if c.User == "" {
		c.User = old.User
	}
	if c.User == "" && e.Getenv("SUDO_USER") != "root" {
		c.User = e.Getenv("SUDO_USER")
	}
	if c.User == "" || c.User == "root" {
		if o.Yes {
			return c, errors.New("couldn't tell which account to run RCM as; pass --user NAME")
		}
		c.User = e.ask("Which user account should RCM run as? (it needs to be able to read your media)", "")
	}
	if c.User == "" || c.User == "root" {
		return c, errors.New("RCM shouldn't run as root; pick a normal user account")
	}
	if _, err := e.LookupUser(c.User); err != nil {
		return c, fmt.Errorf("user %q not found", c.User)
	}

	if len(c.Media) == 0 && !o.Yes {
		c.Media = e.askMedia(old.Media)
	}
	if len(c.Media) == 0 && o.Yes {
		c.Media = old.Media
	}
	var media []string
	for _, m := range c.Media {
		if clean, ok := e.checkMediaFolder(m, c.User); ok {
			media = append(media, clean)
		}
	}
	c.Media = media

	if c.ImportDir == "" {
		def := old.ImportDir
		if def == "" && len(c.Media) > 0 {
			def = filepath.Join(c.Media[0], "RCM Imports")
		}
		if def == "" {
			def = filepath.Join(DataDir, "media")
		}
		c.ImportDir = def
		if !o.Yes {
			e.say("\nDrag-and-drop imports are copied into one folder, sorted into Movies, TV, Music and so on.")
			c.ImportDir = e.ask("Import folder", def)
		}
	}
	if !filepath.IsAbs(c.ImportDir) {
		return c, fmt.Errorf("import folder must be a full path like /media/drive/RCM Imports")
	}
	c.ImportDir = filepath.Clean(c.ImportDir)

	if c.Port == 0 {
		c.Port = old.Port
	}
	if c.Port == 0 {
		c.Port = 8080
	}
	for !o.Yes {
		s := e.ask("Web port", strconv.Itoa(c.Port))
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 65535 {
			e.warn("enter a number from 1 to 65535")
			continue
		}
		c.Port = n
		break
	}
	// An existing RCM holds its own port, so only check a port that's changing.
	if (!existing || c.Port != old.Port) && !e.PortFree(c.Port) {
		return c, fmt.Errorf("port %d is already in use by another program; re-run with --port", c.Port)
	}
	return c, nil
}

// mountSuggestions lists mounted drives under /media, /mnt and /srv, the usual places for media.
func (e *Env) mountSuggestions() []string {
	b, err := os.ReadFile(e.p("/proc/mounts"))
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		// /proc/mounts escapes spaces and tabs as octal
		mp := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\134`, `\`).Replace(f[1])
		for _, prefix := range []string{"/media/", "/mnt/", "/srv/"} {
			if strings.HasPrefix(mp, prefix) && !seen[mp] {
				seen[mp] = true
				out = append(out, mp)
			}
		}
	}
	sort.Strings(out)
	return out
}

func (e *Env) askMedia(previous []string) []string {
	e.say("Where is your media? Add each folder RCM should scan (movies, music, books... can all be in one).")
	if out, err := e.Run("lsblk", "-o", "NAME,SIZE,FSTYPE,LABEL,MOUNTPOINT"); err == nil && strings.TrimSpace(out) != "" {
		e.say("\nDrives on this machine:\n%s", strings.TrimRight(out, "\n"))
	}
	sugg := e.mountSuggestions()
	if len(sugg) > 0 {
		e.say("\nMounted drives you might want:")
		for i, s := range sugg {
			e.say("  %d) %s", i+1, s)
		}
	}
	if len(previous) > 0 {
		e.say("\nCurrently configured: %s", strings.Join(previous, ", "))
	}
	e.say("\nType a number from the list or a full folder path. Press Enter on an empty line when done.")
	var media []string
	for {
		line := e.ask(fmt.Sprintf("Media folder %d", len(media)+1), "")
		if line == "" {
			break
		}
		if n, err := strconv.Atoi(line); err == nil && n >= 1 && n <= len(sugg) {
			line = sugg[n-1]
		}
		if _, ok := e.checkMediaFolder(line, ""); !ok {
			continue
		}
		media = append(media, filepath.Clean(line))
	}
	if len(media) == 0 && len(previous) > 0 {
		return previous
	}
	return media
}

// checkMediaFolder verifies a media folder exists and that the service account can read it.
func (e *Env) checkMediaFolder(p, account string) (string, bool) {
	if !filepath.IsAbs(p) {
		e.warn("%q isn't a full path (it should start with /)", p)
		return "", false
	}
	p = filepath.Clean(p)
	fi, err := os.Stat(e.p(p))
	if err != nil || !fi.IsDir() {
		e.warn("%s isn't a folder on this machine (is the drive mounted?)", p)
		return "", false
	}
	if account != "" {
		if _, err := e.LookPath("runuser"); err == nil {
			if _, err := e.Run("runuser", "-u", account, "--", "test", "-r", p, "-a", "-x", p); err != nil {
				e.warn("%s can't read %s; RCM won't see those files until it can", account, p)
			}
		}
	}
	return p, true
}

// ensurePackages installs what RCM leans on, using the system package manager:
// ffmpeg (durations, thumbnails, chapters, subtitles) and avahi, which lets
// other devices reach this machine as NAME.local (Ubuntu Server doesn't include it).
func (e *Env) ensurePackages(yes bool) {
	var missing []string // "ffmpeg", "avahi"
	if _, err := e.LookPath("ffprobe"); err != nil {
		missing = append(missing, "ffmpeg")
	}
	if _, err := e.LookPath("avahi-daemon"); err != nil {
		missing = append(missing, "avahi")
	}
	if len(missing) == 0 {
		e.ok("ffmpeg and avahi already installed")
		return
	}
	type pm struct {
		bin     string
		update  []string
		install []string
		avahi   string
	}
	managers := []pm{
		{"apt-get", []string{"apt-get", "update", "-q"}, []string{"apt-get", "install", "-y", "-q"}, "avahi-daemon"},
		{"dnf", nil, []string{"dnf", "install", "-y"}, "avahi"},
		{"pacman", nil, []string{"pacman", "-S", "--noconfirm", "--needed"}, "avahi"},
		{"zypper", nil, []string{"zypper", "--non-interactive", "install"}, "avahi"},
	}
	for _, m := range managers {
		if _, err := e.LookPath(m.bin); err != nil {
			continue
		}
		var pkgs, why []string
		for _, name := range missing {
			if name == "ffmpeg" {
				pkgs = append(pkgs, "ffmpeg")
				why = append(why, "ffmpeg (thumbnails, durations, subtitles)")
			} else {
				pkgs = append(pkgs, m.avahi)
				why = append(why, "avahi (lets devices open this machine by name)")
			}
		}
		if !yes && !e.confirm("Install "+strings.Join(why, " and ")+"?", true) {
			e.warn("skipped; RCM still works, with fewer features")
			return
		}
		e.step("installing %s (this can take a few minutes)", strings.Join(pkgs, ", "))
		if m.update != nil {
			e.Run(m.update[0], m.update[1:]...) // a failed refresh shouldn't stop the install attempt
		}
		args := append(append([]string{}, m.install[1:]...), pkgs...)
		if out, err := e.Run(m.install[0], args...); err != nil {
			e.warn("package install failed: %v\n%s", err, lastLines(out, 5))
			return
		}
		for _, name := range missing {
			if name == "avahi" && e.HasSystemd {
				e.Run("systemctl", "enable", "--now", "avahi-daemon")
			}
		}
		e.ok("installed %s", strings.Join(pkgs, ", "))
		return
	}
	e.warn("couldn't find a package manager; install ffmpeg (and avahi) yourself for the full feature set")
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// installBinary copies this program to /usr/local/bin/rcm (atomically, so a running copy isn't disturbed).
func (e *Env) installBinary() error {
	dst := e.p(BinPath)
	if same(e.Executable, dst) {
		e.ok("program already at %s", BinPath)
		return nil
	}
	src, err := os.Open(e.Executable)
	if err != nil {
		return fmt.Errorf("can't read this program to copy it: %w", err)
	}
	defer src.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".new"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return fmt.Errorf("can't write %s: %w", BinPath, err)
	}
	_, err = io.Copy(f, src)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, 0o755)
	}
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("can't install %s: %w", BinPath, err)
	}
	e.ok("program installed to %s", BinPath)
	return nil
}

func same(a, b string) bool {
	fa, err1 := os.Stat(a)
	fb, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(fa, fb)
}

func (e *Env) makeDirs(c Config, u *user.User) error {
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	if err := os.MkdirAll(e.p(DataDir), 0o755); err != nil {
		return err
	}
	if err := os.Chown(e.p(DataDir), uid, gid); err != nil {
		return fmt.Errorf("can't hand %s to %s: %w", DataDir, c.User, err)
	}
	e.ok("database folder %s", DataDir)

	imp := e.p(c.ImportDir)
	if _, err := os.Stat(imp); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(imp, 0o755); err != nil {
			return fmt.Errorf("can't create import folder %s: %w", c.ImportDir, err)
		}
		if err := os.Chown(imp, uid, gid); err != nil {
			e.warn("couldn't give %s ownership of %s: %v", c.User, c.ImportDir, err)
		}
		e.ok("import folder %s created", c.ImportDir)
	} else if _, err := e.LookPath("runuser"); err == nil {
		if _, err := e.Run("runuser", "-u", c.User, "--", "test", "-w", c.ImportDir); err != nil {
			e.warn("%s can't write to %s; drag-and-drop imports will fail until it can", c.User, c.ImportDir)
		} else {
			e.ok("import folder %s", c.ImportDir)
		}
	}
	return nil
}

func (e *Env) writeFile(abs, content string, mode os.FileMode) error {
	p := e.p(abs)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".new"
	if err := os.WriteFile(tmp, []byte(content), mode); err != nil {
		return fmt.Errorf("can't write %s: %w", abs, err)
	}
	return os.Rename(tmp, p)
}

// raiseWatchLimit lets the file watcher cover big libraries (Linux allows few watches by default).
func (e *Env) raiseWatchLimit() {
	if err := e.writeFile(SysctlPath, "# Rough Country Media watches every media folder for new files.\nfs.inotify.max_user_watches=524288\n", 0o644); err != nil {
		e.warn("couldn't raise the file-watch limit: %v", err)
		return
	}
	if _, err := e.Run("sysctl", "-q", "-p", SysctlPath); err != nil {
		e.warn("file-watch limit saved; it takes effect after a reboot")
		return
	}
	e.ok("file-watch limit raised for big libraries")
}

// openFirewall opens the web port if ufw or firewalld is active.
func (e *Env) openFirewall(port int, yes bool) {
	rule := fmt.Sprintf("%d/tcp", port)
	if _, err := e.LookPath("ufw"); err == nil {
		if out, _ := e.Run("ufw", "status"); strings.Contains(out, "Status: active") {
			if yes || e.confirm(fmt.Sprintf("The firewall (ufw) is on. Allow port %d so other devices can reach RCM?", port), true) {
				if _, err := e.Run("ufw", "allow", rule, "comment", "Rough Country Media"); err != nil {
					e.warn("couldn't open port %d in ufw: %v", port, err)
				} else {
					e.ok("firewall: port %d open", port)
				}
			}
			return
		}
	}
	if _, err := e.LookPath("firewall-cmd"); err == nil {
		if out, _ := e.Run("firewall-cmd", "--state"); strings.TrimSpace(out) == "running" {
			if yes || e.confirm(fmt.Sprintf("The firewall (firewalld) is on. Allow port %d so other devices can reach RCM?", port), true) {
				_, err1 := e.Run("firewall-cmd", "--permanent", "--add-port="+rule)
				_, err2 := e.Run("firewall-cmd", "--reload")
				if err := errors.Join(err1, err2); err != nil {
					e.warn("couldn't open port %d in firewalld: %v", port, err)
				} else {
					e.ok("firewall: port %d open", port)
				}
			}
		}
	}
}

func (e *Env) startService() error {
	if !e.HasSystemd {
		e.warn("systemd isn't running here, so RCM won't start at boot.")
		e.say("    Start it by hand with: %s -addr :PORT -data %s -import FOLDER", BinPath, DataDir)
		return nil
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", "rcm"}, {"restart", "rcm"}} {
		if out, err := e.Run("systemctl", args...); err != nil {
			return fmt.Errorf("systemctl %s failed: %v\n%s", strings.Join(args, " "), err, lastLines(out, 5))
		}
	}
	e.ok("service enabled; it starts automatically at boot")
	return nil
}

func (e *Env) waitHealthy(port int) error {
	e.step("waiting for RCM to start")
	url := e.BaseURL(port) + "/api/status"
	for i := 0; i < 30; i++ {
		resp, err := e.HTTP.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				e.ok("RCM is running")
				return nil
			}
		}
		e.Sleep(500 * time.Millisecond)
	}
	logs, _ := e.Run("journalctl", "-u", "rcm", "-n", "20", "--no-pager")
	return fmt.Errorf("RCM didn't respond on port %d. Recent log:\n%s", port, lastLines(logs, 20))
}

// addLibraries registers media folders through the running server, so the
// Settings page stays the one place they're managed.
func (e *Env) addLibraries(c Config) {
	for _, m := range c.Media {
		body, _ := json.Marshal(map[string]string{"path": m})
		resp, err := e.HTTP.Post(e.BaseURL(c.Port)+"/api/libraries", "application/json", bytes.NewReader(body))
		if err != nil {
			e.warn("couldn't add %s: %v (add it in Settings)", m, err)
			continue
		}
		var res struct {
			Error string `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&res)
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			e.warn("couldn't add %s: %s", m, res.Error)
			continue
		}
		e.ok("scanning %s", m)
	}
}

func (e *Env) finish(port int) {
	e.say("\nAll set! Open Rough Country Media from any device on your network:")
	host, _ := e.Hostname()
	for _, u := range LANURLs(host, port) {
		if !strings.Contains(u, "localhost") {
			e.say("  %s", u)
		}
	}
	e.say("\nUseful commands:")
	e.say("  sudo systemctl status rcm      is it running?")
	e.say("  journalctl -u rcm -f           watch the log")
	e.say("  sudo rcm install               upgrade (run a newer rcm the same way)")
	e.say("  sudo rcm install --reconfigure change folders or port")
	e.say("  sudo rcm uninstall             remove RCM (your media is never touched)")
}

// upgrade replaces the program and restarts the service, keeping all settings.
func (e *Env) upgrade(c Config) error {
	e.say("Found an existing install (port %d, running as %s). Upgrading and keeping your settings.", c.Port, c.User)
	e.say("To change folders or the port instead, run: sudo rcm install --reconfigure\n")
	if err := e.installBinary(); err != nil {
		return err
	}
	group := c.User
	if u, err := e.LookupUser(c.User); err == nil {
		if g, err := user.LookupGroupId(u.Gid); err == nil {
			group = g.Name
		}
	}
	if err := e.writeFile(UnitPath, unitFile(c, group), 0o644); err != nil {
		return err
	}
	if err := e.startService(); err != nil {
		return err
	}
	if !e.HasSystemd {
		return nil
	}
	if err := e.waitHealthy(c.Port); err != nil {
		return err
	}
	e.say("\nUpgraded to %s.", e.Version)
	e.finish(c.Port)
	return nil
}

// ---- uninstall ----

func (e *Env) Uninstall(o Options) error {
	if err := e.preflight(); err != nil {
		return err
	}
	c, _ := e.loadConfig()
	e.say("\nThis removes the RCM program and its service. Your media folders are never touched.")
	if !o.Yes && !e.confirm("Uninstall Rough Country Media?", false) {
		return errCancelled
	}
	if e.HasSystemd {
		e.Run("systemctl", "disable", "--now", "rcm")
	}
	for _, p := range []string{UnitPath, BinPath, SysctlPath, ConfPath} {
		if err := os.Remove(e.p(p)); err == nil {
			e.ok("removed %s", p)
		}
	}
	os.Remove(e.p(filepath.Dir(ConfPath)))
	if e.HasSystemd {
		e.Run("systemctl", "daemon-reload")
	}
	purge := o.Purge
	if !purge && !o.Yes {
		purge = e.confirm(fmt.Sprintf("Also delete RCM's database and cache in %s? (watch history, thumbnails; not your media)", DataDir), false)
	}
	if purge {
		if err := os.RemoveAll(e.p(DataDir)); err == nil {
			e.ok("removed %s", DataDir)
		}
	} else {
		e.say("  Kept %s, so reinstalling picks up where you left off.", DataDir)
	}
	if c.ImportDir != "" {
		e.say("  Kept your import folder %s.", c.ImportDir)
	}
	if c.Port != 0 {
		e.say("  If you opened port %d in a firewall, you can close it again.", c.Port)
	}
	e.say("\nRough Country Media is uninstalled.")
	return nil
}

// ---- addresses ----

// LANURLs lists the addresses people on the network can open.
func LANURLs(hostname string, port int) []string {
	ps := strconv.Itoa(port)
	urls := []string{"http://localhost:" + ps}
	if hostname != "" {
		urls = append(urls, "http://"+strings.TrimSuffix(hostname, ".local")+".local:"+ps)
	}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLoopback() && !ipn.IP.IsLinkLocalUnicast() {
			urls = append(urls, "http://"+ipn.IP.String()+":"+ps)
		}
	}
	return urls
}
