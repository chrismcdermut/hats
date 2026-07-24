#!/usr/bin/env node
/**
 * hats — identity profiles for agents and shells.
 * direnv scopes environments to directories; hats scopes them to identities.
 *
 * Profiles live in ~/.config/hats/profiles.json:
 * {
 *   "profiles": {
 *     "acme": {
 *       "description": "Acme Corp (client)",
 *       "env": { "CLAUDE_CONFIG_DIR": "~/.claude-acme", ... },
 *       "path_prepend": ["~/.local/bin"],
 *       "doctor": { "gws dir": "~/.config/gws-acme", ... }   // label -> path that must exist & be non-empty
 *     }
 *   }
 * }
 *
 * Commands:
 *   hats ls                        list profiles
 *   hats env <profile>             print eval-able export lines
 *   hats run <profile> -- <cmd>    run a command under an identity
 *   hats shell <profile>           open a subshell under an identity
 *   hats which                     show the active profile (if any)
 *   hats doctor [profile]          check each profile's credential dirs
 */
'use strict';

const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawnSync } = require('child_process');

const CONFIG_PATH = path.join(
  process.env.HATS_CONFIG || path.join(os.homedir(), '.config', 'hats'),
  'profiles.json'
);

function expand(p) {
  if (typeof p !== 'string') return p;
  return p
    .replace(/^~(?=\/|$)/, os.homedir())
    .replace(/\$HOME/g, os.homedir())
    .replace(/\$\{HOME\}/g, os.homedir());
}

function loadConfig() {
  let raw;
  try {
    raw = fs.readFileSync(CONFIG_PATH, 'utf8');
  } catch {
    die(`no config at ${CONFIG_PATH}\nCreate it with a "profiles" object (see README).`);
  }
  try {
    return JSON.parse(raw);
  } catch (e) {
    die(`config is not valid JSON: ${e.message}`);
  }
}

function getProfile(cfg, name) {
  const prof = (cfg.profiles || {})[name];
  if (!prof) {
    die(`unknown profile '${name}'. Available: ${Object.keys(cfg.profiles || {}).join(', ') || '(none)'}`);
  }
  return prof;
}

function buildEnv(name, prof) {
  const env = { ...process.env };
  for (const [k, v] of Object.entries(prof.env || {})) env[k] = expand(v);
  const prepend = (prof.path_prepend || []).map(expand);
  if (prepend.length) env.PATH = prepend.join(':') + ':' + (env.PATH || '');
  env.HATS_PROFILE = name;
  return env;
}

function die(msg, code = 1) {
  process.stderr.write(`hats: ${msg}\n`);
  process.exit(code);
}

function cmdLs(cfg) {
  const profiles = cfg.profiles || {};
  const names = Object.keys(profiles);
  if (!names.length) return console.log('(no profiles)');
  const width = Math.max(...names.map((n) => n.length));
  for (const n of names) {
    const active = process.env.HATS_PROFILE === n ? '* ' : '  ';
    console.log(`${active}${n.padEnd(width)}  ${profiles[n].description || ''}`);
  }
}

function cmdEnv(cfg, name) {
  const prof = getProfile(cfg, name);
  for (const [k, v] of Object.entries(prof.env || {})) {
    console.log(`export ${k}=${JSON.stringify(expand(v))}`);
  }
  const prepend = (prof.path_prepend || []).map(expand);
  if (prepend.length) console.log(`export PATH=${JSON.stringify(prepend.join(':'))}":$PATH"`);
  console.log(`export HATS_PROFILE=${JSON.stringify(name)}`);
}

function cmdRun(cfg, name, argv) {
  if (!argv.length) die('nothing to run. Usage: hats run <profile> -- <command...>');
  const prof = getProfile(cfg, name);
  const res = spawnSync(argv[0], argv.slice(1), {
    stdio: 'inherit',
    env: buildEnv(name, prof),
  });
  if (res.error) die(res.error.message, 127);
  process.exit(res.status === null ? 1 : res.status);
}

function cmdShell(cfg, name) {
  const prof = getProfile(cfg, name);
  const shell = process.env.SHELL || '/bin/zsh';
  console.error(`hats: entering '${name}' shell (exit to leave)`);
  const res = spawnSync(shell, [], { stdio: 'inherit', env: buildEnv(name, prof) });
  process.exit(res.status === null ? 0 : res.status);
}

function cmdWhich() {
  console.log(process.env.HATS_PROFILE || '(none)');
}

function checkPath(p) {
  const full = expand(p);
  try {
    const st = fs.statSync(full);
    if (st.isDirectory()) {
      return fs.readdirSync(full).length > 0 ? 'ok' : 'empty';
    }
    return st.size > 0 ? 'ok' : 'empty';
  } catch {
    return 'missing';
  }
}

function cmdDoctor(cfg, name) {
  const targets = name ? [name] : Object.keys(cfg.profiles || {});
  let bad = 0;
  for (const n of targets) {
    const prof = getProfile(cfg, n);
    console.log(`\n${n}${prof.description ? '  — ' + prof.description : ''}`);
    const checks = prof.doctor || {};
    if (!Object.keys(checks).length) {
      console.log('  (no doctor checks defined)');
      continue;
    }
    const width = Math.max(...Object.keys(checks).map((k) => k.length));
    for (const [label, p] of Object.entries(checks)) {
      const state = checkPath(p);
      const icon = state === 'ok' ? '✓' : state === 'empty' ? '○' : '✗';
      if (state !== 'ok') bad++;
      console.log(`  ${icon} ${label.padEnd(width)}  ${expand(p)}${state !== 'ok' ? `  [${state}]` : ''}`);
    }
  }
  console.log(
    `\n${bad === 0 ? 'all checks passed' : bad + ' check(s) not ok (○ empty = not yet logged in, ✗ missing = path absent)'}`
  );
  process.exit(bad === 0 ? 0 : 1);
}

function usage() {
  console.log(`hats — identity profiles for agents and shells

usage:
  hats ls                        list profiles (* = active)
  hats env <profile>             print eval-able exports
  hats run <profile> -- <cmd>    run command under an identity
  hats shell <profile>           subshell under an identity
  hats which                     active profile
  hats doctor [profile]          check credential dirs

config: ${CONFIG_PATH}`);
}

function main() {
  const args = process.argv.slice(2);
  const cmd = args[0];
  if (!cmd || cmd === '-h' || cmd === '--help' || cmd === 'help') return usage();
  if (cmd === 'which') return cmdWhich();
  const cfg = loadConfig();
  switch (cmd) {
    case 'ls':
    case 'list':
      return cmdLs(cfg);
    case 'env':
      return cmdEnv(cfg, args[1] || die('usage: hats env <profile>'));
    case 'run': {
      const name = args[1] || die('usage: hats run <profile> -- <command...>');
      const sep = args.indexOf('--');
      return cmdRun(cfg, name, sep === -1 ? args.slice(2) : args.slice(sep + 1));
    }
    case 'shell':
      return cmdShell(cfg, args[1] || die('usage: hats shell <profile>'));
    case 'doctor':
      return cmdDoctor(cfg, args[1]);
    default:
      die(`unknown command '${cmd}' (try: hats help)`);
  }
}

main();
