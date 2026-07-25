#!/usr/bin/env node
'use strict';
/**
 * manyhats — thin launcher around the prebuilt `hats` Go binary.
 * There is one implementation (Go); this npm package just distributes it, so
 * `npm i -g manyhats` never drifts from `brew`/`go install`.
 */
const { spawnSync } = require('child_process');
const { ensureBinary } = require('./install');

(async () => {
  let bin;
  try {
    bin = await ensureBinary();
  } catch (err) {
    console.error(err.message);
    process.exit(1);
  }
  const res = spawnSync(bin, process.argv.slice(2), { stdio: 'inherit' });
  if (res.error) {
    console.error(`manyhats: failed to run hats: ${res.error.message}`);
    process.exit(1);
  }
  process.exit(res.status === null ? 1 : res.status);
})();
