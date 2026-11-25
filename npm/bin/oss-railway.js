#!/usr/bin/env node
'use strict';

const { spawn } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');

const INSTALL_URL =
  process.env.OSSR_INSTALL_URL ||
  'https://raw.githubusercontent.com/KumarRoushan9234/OSS-Railway/main/install.sh';
const DRY_RUN = process.env.OSSR_DRY_RUN === '1';

const USAGE = `Usage:
  oss-railway setup <user@host> [-p port] [-i identityFile]   install on a server over SSH
  oss-railway install                                         install on this Linux server (as root)
  oss-railway help                                            show this help`;

// user: letters, digits, dot, dash, underscore (not leading dash).
// host: IPv4 or hostname labels of letters, digits, dash (not leading dash).
const TARGET_RE =
  /^([A-Za-z0-9_][A-Za-z0-9._-]{0,31})@((?:\d{1,3}\.){3}\d{1,3}|[A-Za-z0-9](?:[A-Za-z0-9-]{0,62})(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]{0,62}))*)$/;

function fail(msg) {
  console.error(`oss-railway: ${msg}`);
  process.exit(2);
}

function run(cmd, args) {
  if (DRY_RUN) {
    console.log([cmd, ...args].map(quote).join(' '));
    process.exit(0);
  }
  const child = spawn(cmd, args, { stdio: 'inherit' });
  child.on('error', (err) => fail(`could not run ${cmd}: ${err.message}`));
  child.on('exit', (code, signal) => process.exit(signal ? 1 : code ?? 1));
}

// Display-only quoting for dry-run output.
function quote(s) {
  return /^[A-Za-z0-9_@%+=:,./-]+$/.test(s) ? s : `'${s.replace(/'/g, `'\\''`)}'`;
}

function shQuote(s) {
  return `'${s.replace(/'/g, `'\\''`)}'`;
}

function setup(argv) {
  let target;
  let port;
  let identity;
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === '-p' || a === '-i') {
      const v = argv[++i];
      if (v === undefined) fail(`${a} needs a value`);
      if (a === '-p') {
        if (!/^\d{1,5}$/.test(v) || +v < 1 || +v > 65535) fail(`invalid port: ${v}`);
        port = v;
      } else {
        if (v.startsWith('-')) fail(`invalid identity file: ${v}`);
        identity = v;
      }
    } else if (target === undefined && TARGET_RE.test(a)) {
      const ip = a.split('@')[1];
      if (/^[\d.]+$/.test(ip) && ip.split('.').some((n) => +n > 255)) fail(`invalid host: ${a}`);
      target = a;
    } else {
      fail(`invalid argument: ${a}\n${USAGE}`);
    }
  }
  if (!target) fail(`missing <user@host>\n${USAGE}`);

  const user = target.split('@')[0];
  const sh = user === 'root' ? 'sh' : 'sudo sh';
  const remote = `curl -fsSL ${shQuote(INSTALL_URL)} | ${sh}`;

  const args = [];
  if (port) args.push('-p', port);
  if (identity) args.push('-i', identity);
  args.push('--', target, remote);
  run('ssh', args);
}

async function install() {
  const isRoot = typeof process.getuid === 'function' && process.getuid() === 0;
  if (process.platform !== 'linux' || !isRoot) {
    console.log('Run this as root on the Linux server, or from your machine:');
    console.log('  npx oss-railway setup root@<server-ip>');
    process.exit(0);
  }
  if (DRY_RUN) return run('sh', ['<downloaded install.sh>']);

  const res = await fetch(INSTALL_URL);
  if (!res.ok) fail(`download failed: HTTP ${res.status} ${INSTALL_URL}`);
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'ossr-'));
  const file = path.join(dir, 'install.sh');
  fs.writeFileSync(file, Buffer.from(await res.arrayBuffer()), { mode: 0o700 });
  process.on('exit', () => fs.rmSync(dir, { recursive: true, force: true }));
  run('sh', [file]);
}

const [cmd, ...rest] = process.argv.slice(2);
switch (cmd) {
  case 'setup':
    setup(rest);
    break;
  case 'install':
    install().catch((err) => fail(err.message));
    break;
  case undefined:
  case 'help':
  case '-h':
  case '--help':
    console.log(USAGE);
    break;
  default:
    console.error(USAGE);
    process.exit(2);
}
