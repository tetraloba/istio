# created by tetraloba (2026/07/24 12:45)
# hugelobaのistioリポジトリにおいてistio.depsファイルを更新するためのスクリプト
GIT_REMOTE_URL="https://github.com/tetraloba/proxy.git"
BRANCH="feat/tetraloba"

ISTIO_PROXY_SHA=$(git ls-remote ${GIT_REMOTE_URL} ${BRANCH} | cut -f 1)
cp istio.deps istio.deps.old
jq ".[0].lastStableSHA|=\"${ISTIO_PROXY_SHA}\"" istio.deps.old > istio.deps
diff istio.deps.old istio.deps # debug

