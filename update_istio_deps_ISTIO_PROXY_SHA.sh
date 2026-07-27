# created by tetraloba (2026/07/24 12:45)
# hugelobaのistioリポジトリにおいてistio.depsファイルを更新するためのスクリプト

set -e
SCRIPT_DIR=$(cd $(dirname $0); pwd)
ISTIO_DEPS_FILE="${SCRIPT_DIR}/istio.deps"

GIT_REMOTE_URL="https://github.com/tetraloba/proxy.git"
BRANCH="feat/tetraloba"

OLD_ISTIO_PROXY_SHA=$(jq -r ".[0].lastStableSHA" ${ISTIO_DEPS_FILE})
NEW_ISTIO_PROXY_SHA=$(git ls-remote ${GIT_REMOTE_URL} ${BRANCH} | cut -f 1)

cp ${ISTIO_DEPS_FILE} ${ISTIO_DEPS_FILE}.old

jq ".[0].lastStableSHA|=\"${NEW_ISTIO_PROXY_SHA}\"" ${ISTIO_DEPS_FILE}.old > ${ISTIO_DEPS_FILE}

echo "update proxy ${OLD_ISTIO_PROXY_SHA:0:7} to ${NEW_ISTIO_PROXY_SHA:0:7}" >> ${SCRIPT_DIR}/.commit_messages

diff ${ISTIO_DEPS_FILE}.old ${ISTIO_DEPS_FILE} # debug

git -C ${SCRIPT_DIR} add ${ISTIO_DEPS_FILE} &&
git -C ${SCRIPT_DIR} commit -m "$(tail -n 1 ${SCRIPT_DIR}/.commit_messages)" &&
git -C ${SCRIPT_DIR} push

