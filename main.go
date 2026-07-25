// hats — identity profiles for agents and shells.
// direnv scopes environments to directories; hats scopes them to identities.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
)

// version is stamped at build time via -ldflags "-X main.version=..."
// (release builds get the tag; local builds report "dev").
var version = "dev"

type Profile struct {
	Description string            `json:"description"`
	Env         map[string]string `json:"env"`
	PathPrepend []string          `json:"path_prepend"`
	Doctor      map[string]string `json:"doctor"`
	// Logins maps a short name (e.g. "gws") to the login command run under this
	// hat (e.g. "gws auth login"), so tokens land in this profile's config dir.
	Logins map[string]string `json:"logins"`
	// EnvFiles are gitignored KEY=VALUE files (secrets) sourced ONLY when wearing
	// this hat, so tokens stay identity-scoped instead of global. Loaded before
	// Env (so Env can override). Lines: `export KEY="val"`, `KEY=val`, `# comment`.
	EnvFiles []string `json:"env_files"`
}

type Config struct {
	Profiles map[string]Profile `json:"profiles"`
}

func home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		die("cannot determine home directory: " + err.Error())
	}
	return h
}

func configPath() string {
	dir := os.Getenv("HATS_CONFIG")
	if dir == "" {
		dir = filepath.Join(home(), ".config", "hats")
	}
	return filepath.Join(dir, "profiles.json")
}

func expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		p = home() + p[1:]
	}
	p = strings.ReplaceAll(p, "${HOME}", home())
	p = strings.ReplaceAll(p, "$HOME", home())
	return p
}

func loadConfig() Config {
	raw, err := os.ReadFile(configPath())
	if err != nil {
		die(fmt.Sprintf("no config at %s\nCreate it with a \"profiles\" object (see README).", configPath()))
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		die("config is not valid JSON: " + err.Error())
	}
	return cfg
}

func getProfile(cfg Config, name string) Profile {
	prof, ok := cfg.Profiles[name]
	if !ok {
		die(fmt.Sprintf("unknown profile '%s'. Available: %s", name, strings.Join(profileNames(cfg), ", ")))
	}
	return prof
}

func profileNames(cfg Config) []string {
	names := make([]string, 0, len(cfg.Profiles))
	for n := range cfg.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

var envLineRe = regexp.MustCompile(`^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$`)

// loadEnvFile parses a simple KEY=VALUE / export KEY="VALUE" secrets file.
// Missing files are silently skipped (a secret file may not exist on every machine).
func loadEnvFile(path string) map[string]string {
	out := map[string]string{}
	data, err := os.ReadFile(expand(path))
	if err != nil {
		return out // absent/unreadable: skip quietly
	}
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		m := envLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		val := strings.TrimSpace(m[2])
		// strip one layer of matching quotes
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
			val = val[1 : len(val)-1]
		}
		out[m[1]] = val
	}
	return out
}

// resolveEnv returns the hat's extra environment: env_files first (secrets),
// then Env (which may override), all with ~ / $HOME expanded. PATH/HATS_PROFILE
// are handled separately by callers.
func resolveEnv(prof Profile) map[string]string {
	env := map[string]string{}
	for _, f := range prof.EnvFiles {
		for k, v := range loadEnvFile(f) {
			env[k] = v
		}
	}
	for k, v := range prof.Env {
		env[k] = expand(v)
	}
	return env
}

// applyEnv mutates this process's environment to wear the given hat,
// so a subsequent exec inherits it (and PATH lookups use the new PATH).
func applyEnv(name string, prof Profile) {
	for k, v := range resolveEnv(prof) {
		os.Setenv(k, v)
	}
	if len(prof.PathPrepend) > 0 {
		parts := make([]string, 0, len(prof.PathPrepend))
		for _, p := range prof.PathPrepend {
			parts = append(parts, expand(p))
		}
		os.Setenv("PATH", strings.Join(parts, ":")+":"+os.Getenv("PATH"))
	}
	os.Setenv("HATS_PROFILE", name)
}

func die(msg string) {
	fmt.Fprintf(os.Stderr, "hats: %s\n", msg)
	os.Exit(1)
}

func cmdLs(cfg Config) {
	names := profileNames(cfg)
	if len(names) == 0 {
		fmt.Println("(no profiles)")
		return
	}
	width := 0
	for _, n := range names {
		if len(n) > width {
			width = len(n)
		}
	}
	active := os.Getenv("HATS_PROFILE")
	for _, n := range names {
		marker := "  "
		if n == active {
			marker = "* "
		}
		fmt.Printf("%s%-*s  %s\n", marker, width, n, cfg.Profiles[n].Description)
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func cmdEnv(cfg Config, name string, asJSON bool) {
	prof := getProfile(cfg, name)
	resolved := resolveEnv(prof) // env_files + Env, expanded
	if asJSON {
		prepend := make([]string, 0, len(prof.PathPrepend))
		for _, p := range prof.PathPrepend {
			prepend = append(prepend, expand(p))
		}
		out, _ := json.MarshalIndent(map[string]any{
			"profile":      name,
			"env":          resolved,
			"path_prepend": prepend,
		}, "", "  ")
		fmt.Println(string(out))
		return
	}
	keys := make([]string, 0, len(resolved))
	for k := range resolved {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("export %s=%s\n", k, shellQuote(resolved[k]))
	}
	if len(prof.PathPrepend) > 0 {
		parts := make([]string, 0, len(prof.PathPrepend))
		for _, p := range prof.PathPrepend {
			parts = append(parts, expand(p))
		}
		fmt.Printf("export PATH=%s\":$PATH\"\n", shellQuote(strings.Join(parts, ":")))
	}
	fmt.Printf("export HATS_PROFILE=%s\n", shellQuote(name))
}

// execUnder replaces this process with the command, wearing the hat.
// True exec: signals, tty, and exit status flow naturally.
func execUnder(name string, prof Profile, argv []string) {
	applyEnv(name, prof)
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		die(fmt.Sprintf("command not found: %s", argv[0]))
	}
	if err := syscall.Exec(bin, argv, os.Environ()); err != nil {
		die("exec failed: " + err.Error())
	}
}

func cmdRun(cfg Config, name string, argv []string) {
	if len(argv) == 0 {
		die("nothing to run. Usage: hats run <profile> -- <command...>")
	}
	execUnder(name, getProfile(cfg, name), argv)
}

func cmdShell(cfg Config, name string) {
	prof := getProfile(cfg, name)
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	fmt.Fprintf(os.Stderr, "hats: entering '%s' shell (exit to leave)\n", name)
	execUnder(name, prof, []string{shell})
}

// cmdLogin runs a profile's declared login commands wearing the hat, so each
// CLI writes its token to this identity's config dir. Runs as subprocesses
// (sequential, interactive) rather than exec, since there may be several.
func cmdLogin(cfg Config, name, which string) {
	prof := getProfile(cfg, name)
	if len(prof.Logins) == 0 {
		die(fmt.Sprintf("profile '%s' declares no logins. Add a \"logins\" map to profiles.json, e.g. {\"gws\": \"gws auth login\"}.", name))
	}
	var targets []string
	if which != "" {
		if _, ok := prof.Logins[which]; !ok {
			keys := make([]string, 0, len(prof.Logins))
			for k := range prof.Logins {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			die(fmt.Sprintf("profile '%s' has no login '%s'. Available: %s", name, which, strings.Join(keys, ", ")))
		}
		targets = []string{which}
	} else {
		for k := range prof.Logins {
			targets = append(targets, k)
		}
		sort.Strings(targets)
	}

	// build the hat's environment once
	env := os.Environ()
	extra := resolveEnv(prof) // env_files + Env
	extra["HATS_PROFILE"] = name
	if len(prof.PathPrepend) > 0 {
		parts := make([]string, 0, len(prof.PathPrepend))
		for _, p := range prof.PathPrepend {
			parts = append(parts, expand(p))
		}
		extra["PATH"] = strings.Join(parts, ":") + ":" + os.Getenv("PATH")
	}
	for k, v := range extra {
		env = append(env, k+"="+v)
	}

	failed := 0
	for _, t := range targets {
		cmdline := prof.Logins[t]
		fmt.Fprintf(os.Stderr, "\n\033[1mhats login %s: %s\033[0m  (%s)\n", name, t, cmdline)
		c := exec.Command("sh", "-c", cmdline)
		c.Env = env
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := c.Run(); err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "\033[31m  ✗ %s login failed: %v\033[0m\n", t, err)
		}
	}
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "\n%d login(s) failed. Re-run: hats login %s [name]\n", failed, name)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "\n\033[32mall logins for '%s' completed. Verify with: hats doctor %s\033[0m\n", name, name)
}

func cmdWhich() {
	p := os.Getenv("HATS_PROFILE")
	if p == "" {
		p = "(none)"
	}
	fmt.Println(p)
}

func checkPath(p string) string {
	full := expand(p)
	st, err := os.Stat(full)
	if err != nil {
		return "missing"
	}
	if st.IsDir() {
		entries, err := os.ReadDir(full)
		if err != nil || len(entries) == 0 {
			return "empty"
		}
		return "ok"
	}
	if st.Size() == 0 {
		return "empty"
	}
	return "ok"
}

func cmdDoctor(cfg Config, name string) {
	targets := profileNames(cfg)
	if name != "" {
		getProfile(cfg, name) // validate
		targets = []string{name}
	}
	bad := 0
	for _, n := range targets {
		prof := cfg.Profiles[n]
		desc := ""
		if prof.Description != "" {
			desc = "  — " + prof.Description
		}
		fmt.Printf("\n%s%s\n", n, desc)
		if len(prof.Doctor) == 0 {
			fmt.Println("  (no doctor checks defined)")
			continue
		}
		labels := make([]string, 0, len(prof.Doctor))
		width := 0
		for l := range prof.Doctor {
			labels = append(labels, l)
			if len(l) > width {
				width = len(l)
			}
		}
		sort.Strings(labels)
		for _, label := range labels {
			p := prof.Doctor[label]
			state := checkPath(p)
			icon := "✓"
			suffix := ""
			if state != "ok" {
				bad++
				suffix = "  [" + state + "]"
				if state == "empty" {
					icon = "○"
				} else {
					icon = "✗"
				}
			}
			fmt.Printf("  %s %-*s  %s%s\n", icon, width, label, expand(p), suffix)
		}
	}
	if bad == 0 {
		fmt.Println("\nall checks passed")
		os.Exit(0)
	}
	fmt.Printf("\n%d check(s) not ok (○ empty = not yet logged in, ✗ missing = path absent)\n", bad)
	os.Exit(1)
}

func usage() {
	fmt.Printf(`hats — identity profiles for agents and shells

usage:
  hats ls                        list profiles (* = active)
  hats env <profile> [--json]    print eval-able exports (or JSON for tooling)
  hats run <profile> -- <cmd>    run command under an identity (true exec)
  hats shell <profile>           subshell under an identity
  hats login <profile> [name]    log declared CLIs in, wearing the hat
  hats which                     active profile
  hats doctor [profile]          check credential dirs

config: %s
`, configPath())
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		usage()
		return
	}
	cmd := args[0]
	if cmd == "version" || cmd == "--version" || cmd == "-v" {
		fmt.Println("hats " + version)
		return
	}
	if cmd == "which" {
		cmdWhich()
		return
	}
	cfg := loadConfig()
	switch cmd {
	case "ls", "list":
		cmdLs(cfg)
	case "env":
		rest := args[1:]
		asJSON := false
		name := ""
		for _, a := range rest {
			if a == "--json" {
				asJSON = true
			} else if name == "" {
				name = a
			}
		}
		if name == "" {
			die("usage: hats env <profile> [--json]")
		}
		cmdEnv(cfg, name, asJSON)
	case "run":
		if len(args) < 2 {
			die("usage: hats run <profile> -- <command...>")
		}
		name := args[1]
		argv := args[2:]
		if len(argv) > 0 && argv[0] == "--" {
			argv = argv[1:]
		}
		cmdRun(cfg, name, argv)
	case "shell":
		if len(args) < 2 {
			die("usage: hats shell <profile>")
		}
		cmdShell(cfg, args[1])
	case "login":
		if len(args) < 2 {
			die("usage: hats login <profile> [name]   (logs each declared CLI in, wearing the hat)")
		}
		which := ""
		if len(args) > 2 {
			which = args[2]
		}
		cmdLogin(cfg, args[1], which)
	case "doctor":
		name := ""
		if len(args) > 1 {
			name = args[1]
		}
		cmdDoctor(cfg, name)
	default:
		die(fmt.Sprintf("unknown command '%s' (try: hats help)", cmd))
	}
}
