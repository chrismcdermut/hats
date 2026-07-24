// hats — identity profiles for agents and shells.
// direnv scopes environments to directories; hats scopes them to identities.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// applyEnv mutates this process's environment to wear the given hat,
// so a subsequent exec inherits it (and PATH lookups use the new PATH).
func applyEnv(name string, prof Profile) {
	for k, v := range prof.Env {
		os.Setenv(k, expand(v))
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
	if asJSON {
		resolved := map[string]string{}
		for k, v := range prof.Env {
			resolved[k] = expand(v)
		}
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
	keys := make([]string, 0, len(prof.Env))
	for k := range prof.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("export %s=%s\n", k, shellQuote(expand(prof.Env[k])))
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
