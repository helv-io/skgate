#!/bin/bash
# Everything the browser tests need, installed once into a cache and printed as exports, so one command runs what CI runs:
#   eval "$(tools/browser-env.sh)"        (CI: tools/browser-env.sh --github-env)
# Without these variables the browser tests skip, a local run looks green and CI goes red (699012c).
set -euo pipefail
cache="${SKGATE_BROWSER_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/skgate-browser}"
mkdir -p "$cache/jsd" "$cache/pp"
command -v node >/dev/null || { echo "echo 'error: node is needed for the browser tests' >&2; false"; exit 0; }
[ -d "$cache/jsd/node_modules/jsdom" ] || (cd "$cache/jsd" && npm init -y >/dev/null && npm install --silent jsdom@24 >&2)
[ -d "$cache/pp/node_modules/puppeteer-core" ] || (cd "$cache/pp" && npm init -y >/dev/null && npm install --silent puppeteer-core >&2)
chrome=$(command -v google-chrome || command -v chromium || command -v chromium-browser || true)
[ -n "$chrome" ] || { echo "echo 'error: no Chrome or Chromium found for the browser tests' >&2; false"; exit 0; }
if [ "${1:-}" = "--github-env" ]; then
  { echo "SKGATE_JSDOM=$cache/jsd"; echo "SKGATE_PUPPETEER=$cache/pp"; echo "SKGATE_CHROME=$chrome"; } >> "$GITHUB_ENV"
else
  echo "export SKGATE_JSDOM='$cache/jsd' SKGATE_PUPPETEER='$cache/pp' SKGATE_CHROME='$chrome'"
fi
