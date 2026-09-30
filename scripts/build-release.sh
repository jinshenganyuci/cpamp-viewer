#!/usr/bin/env bash
set -euo pipefail

export LC_ALL=C
umask 022

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION_FILE="${ROOT_DIR}/VERSION"
PROJECT_VERSION="$(tr -d '\r\n' < "${VERSION_FILE}")"
VERSION="${VERSION:-${PROJECT_VERSION}}"
SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-0}"

if [[ ! "${PROJECT_VERSION}" =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
  echo "Invalid VERSION file: ${PROJECT_VERSION}" >&2
  exit 1
fi
if [[ "${VERSION}" != "${PROJECT_VERSION}" ]]; then
  echo "VERSION override (${VERSION}) must match ${VERSION_FILE} (${PROJECT_VERSION})." >&2
  exit 1
fi
if [[ ! "${SOURCE_DATE_EPOCH}" =~ ^[0-9]+$ ]]; then
  echo "SOURCE_DATE_EPOCH must be a non-negative integer." >&2
  exit 1
fi

NODE_IMAGE="node:24-alpine@sha256:a0b9bf06e4e6193cf7a0f58816cc935ff8c2a908f81e6f1a95432d679c54fbfd"
GO_IMAGE="golang:1.26-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2"
IMAGE="cpamp-viewer:${VERSION}"
OUT_DIR="${ROOT_DIR}/release/${VERSION}"
WINDOWS_BASENAME="cpamp-viewer_${VERSION}_windows_amd64"
WINDOWS_DIR="${OUT_DIR}/${WINDOWS_BASENAME}"
DEPLOYMENT_BASENAME="cpamp-viewer_${VERSION}_deployment"
DEPLOYMENT_DIR="${OUT_DIR}/${DEPLOYMENT_BASENAME}"
LICENSE_STAGE="${OUT_DIR}/licenses"
IMAGE_ARCHIVE_NAME="cpamp-viewer_${VERSION}_linux_amd64.tar.gz"
IMAGE_ARCHIVE="${OUT_DIR}/${IMAGE_ARCHIVE_NAME}"
WINDOWS_ARCHIVE="${OUT_DIR}/${WINDOWS_BASENAME}.tar.gz"
DEPLOYMENT_ARCHIVE="${OUT_DIR}/${DEPLOYMENT_BASENAME}.tar.gz"
LICENSE_CONTAINER_ID=""

cleanup() {
  if [[ -n "${LICENSE_CONTAINER_ID}" ]]; then
    docker rm -f "${LICENSE_CONTAINER_ID}" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

for command_name in docker sha256sum tar gzip find sort grep id; do
  if ! command -v "${command_name}" >/dev/null 2>&1; then
    echo "Missing required command: ${command_name}" >&2
    exit 1
  fi
done

case "${OUT_DIR}" in
  "${ROOT_DIR}"/release/*) ;;
  *)
    echo "Refusing to clean unsafe output path: ${OUT_DIR}" >&2
    exit 1
    ;;
esac

rm -rf -- "${OUT_DIR}"
mkdir -p "${WINDOWS_DIR}" "${DEPLOYMENT_DIR}" "${LICENSE_STAGE}"

echo "[1/9] Validate Compose files and Dockerfile"
CPAMP_VIEWER_VERSION="${VERSION}" \
CLIPROXY_NETWORK=bridge \
CPAMP_ADMIN_KEY_PATH=/dev/null \
VIEWER_SESSION_SECRET_PATH=/dev/null \
  docker compose -f "${ROOT_DIR}/compose.yaml" config --quiet
CPAMP_VIEWER_VERSION="${VERSION}" \
CLIPROXY_NETWORK=bridge \
CPAMP_ADMIN_KEY_PATH=/dev/null \
VIEWER_SESSION_SECRET_PATH=/dev/null \
  docker compose -f "${ROOT_DIR}/docker-compose.viewer.yml" config --quiet
CPAMP_VIEWER_VERSION="${VERSION}" \
CLIPROXY_NETWORK=bridge \
CPAMP_ADMIN_KEY_PATH=/dev/null \
VIEWER_SESSION_SECRET_PATH=/dev/null \
  docker compose -f "${ROOT_DIR}/docker-compose.acceptance.yml" config --quiet
for compose_pair in "docker-compose.viewer.yml docker-compose.access-guard.yml" "docker-compose.acceptance.yml docker-compose.access-guard.acceptance.yml"; do
  read -r base_compose plugin_compose <<< "${compose_pair}"
  CPAMP_VIEWER_VERSION="${VERSION}" CLIPROXY_NETWORK=bridge \
  CPAMP_ADMIN_KEY_PATH=/dev/null VIEWER_SESSION_SECRET_PATH=/dev/null \
  ACCESS_GUARD_BASE_URL=http://example.invalid:8317 \
  ACCESS_GUARD_MANAGEMENT_KEY_PATH=/dev/null ACCESS_GUARD_PUBLIC_KEYS_PATH=/dev/null \
    docker compose -f "${ROOT_DIR}/${base_compose}" -f "${ROOT_DIR}/${plugin_compose}" config --quiet
done
for base_compose in compose.yaml docker-compose.viewer.yml; do
  CPAMP_VIEWER_VERSION="${VERSION}" CLIPROXY_NETWORK=bridge \
  CPAMP_ADMIN_KEY_PATH=/dev/null VIEWER_SESSION_SECRET_PATH=/dev/null \
  SUB2API_BASE_URL=http://sub2api.example.invalid:8080 SUB2API_ADMIN_API_KEY_PATH=/dev/null \
    docker compose -f "${ROOT_DIR}/${base_compose}" -f "${ROOT_DIR}/compose.sub2api.yaml" config --quiet
done
docker buildx build --check \
  --platform linux/amd64 \
  --build-arg VERSION="${VERSION}" \
  --build-arg SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH}" \
  "${ROOT_DIR}"

echo "[2/9] Build and test the frontend with the pinned Node image"
docker run --rm --platform linux/amd64 \
  --user "$(id -u):$(id -g)" \
  -e CI=1 \
  -e HOME=/tmp/home \
  -e npm_config_cache=/tmp/npm-cache \
  -e VERSION="${VERSION}" \
  -v "${ROOT_DIR}/web-cpamp:/workspace/web-cpamp" \
  -v "${ROOT_DIR}/server:/workspace/server" \
  -w /workspace/web-cpamp \
  "${NODE_IMAGE}" \
  sh -ceu 'node -e '\''const p=require("./package.json"),l=require("./package-lock.json"),v=process.env.VERSION;if(p.version!==v||l.version!==v||l.packages[""].version!==v){throw new Error(`frontend package version mismatch: ${p.version}/${l.version}/${l.packages[""].version} != ${v}`)}'\''; npm ci --ignore-scripts --no-audit --no-fund; npm run type-check; npm run lint; npm run test; npm run build'
test -s "${ROOT_DIR}/server/webdist/index.html"
if ! grep -R -I -l -F -- "${VERSION}" "${ROOT_DIR}/server/webdist" >/dev/null; then
  echo "Built Viewer bundle does not contain release version ${VERSION}." >&2
  exit 1
fi
for forbidden_text in \
  managementKey \
  /api-call \
  usage/import \
  reset-credit \
  /v0/management/model-prices \
  /v0/management/api-key-aliases \
  /v0/management/plugins/access-guard \
  ACCESS_GUARD_MANAGEMENT_KEY \
  reset-quota; do
  if grep -R -I -l -F -- "${forbidden_text}" "${ROOT_DIR}/server/webdist" >/dev/null; then
    echo "Forbidden management capability survived the Viewer bundle: ${forbidden_text}" >&2
    exit 1
  fi
done

echo "[3/9] Format-check and test the Go server"
docker run --rm --platform linux/amd64 \
  -v "${ROOT_DIR}/server:/src:ro" \
  -w /src \
  "${GO_IMAGE}" \
  sh -ceu 'test -z "$(/usr/local/go/bin/gofmt -l .)"; /usr/local/go/bin/go test -buildvcs=false -mod=readonly ./...'

echo "[4/9] Cross-compile deterministic Windows amd64 binaries"
docker run --rm --platform linux/amd64 \
  -v "${ROOT_DIR}/server:/src/server:ro" \
  -v "${ROOT_DIR}/tests:/src/tests:ro" \
  -v "${WINDOWS_DIR}:/out" \
  -w /src/server \
  -e CGO_ENABLED=0 \
  -e GOOS=windows \
  -e GOARCH=amd64 \
  "${GO_IMAGE}" \
  sh -ceu '/usr/local/go/bin/go build -buildvcs=false -mod=readonly -trimpath -ldflags="-s -w" -o /out/cpamp-viewer.exe .; /usr/local/go/bin/go build -buildvcs=false -mod=readonly -trimpath -ldflags="-s -w" -o /out/cpamp-viewer-healthcheck.exe ./cmd/healthcheck; /usr/local/go/bin/go build -buildvcs=false -trimpath -ldflags="-s -w" -o /out/mock-cpamp.exe /src/tests/mock-cpamp/main.go'
cp "${WINDOWS_DIR}/cpamp-viewer.exe" "${WINDOWS_DIR}/cpamp-viewer-remote-test.exe"

echo "[5/9] Build the linux/amd64 image"
BUILDX_NO_DEFAULT_ATTESTATIONS=1 docker buildx build --load \
  --platform linux/amd64 \
  --provenance=false \
  --sbom=false \
  --build-arg VERSION="${VERSION}" \
  --build-arg SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH}" \
  -t "${IMAGE}" \
  "${ROOT_DIR}"

image_platform="$(docker image inspect "${IMAGE}" --format '{{.Os}}/{{.Architecture}}')"
image_version="$(docker image inspect "${IMAGE}" --format '{{index .Config.Labels "org.opencontainers.image.version"}}')"
image_user="$(docker image inspect "${IMAGE}" --format '{{.Config.User}}')"
if [[ "${image_platform}" != "linux/amd64" ]]; then
  echo "Unexpected image platform: ${image_platform}" >&2
  exit 1
fi
if [[ "${image_version}" != "${VERSION}" ]]; then
  echo "Unexpected image version label: ${image_version}" >&2
  exit 1
fi
if [[ "${image_user}" != "nonroot:nonroot" ]]; then
  echo "Unexpected image user: ${image_user}" >&2
  exit 1
fi

echo "[6/9] Save the offline image and verify embedded licenses"
docker image save "${IMAGE}" | gzip -n -9 > "${IMAGE_ARCHIVE}"
(
  cd "${OUT_DIR}"
  sha256sum "${IMAGE_ARCHIVE_NAME}" > "${IMAGE_ARCHIVE_NAME}.sha256"
)

LICENSE_CONTAINER_ID="$(docker create "${IMAGE}")"
docker cp "${LICENSE_CONTAINER_ID}:/licenses/." "${LICENSE_STAGE}/"
docker rm "${LICENSE_CONTAINER_ID}" >/dev/null
LICENSE_CONTAINER_ID=""
for license_file in \
  CPAMP-Viewer-LICENSE \
  CPA-Manager-Plus-LICENSE \
  CPA-Manager-Plus-UPSTREAM.md \
  ECharts-LICENSE \
  ECharts-NOTICE \
  zrender-LICENSE \
  React-LICENSE \
  React-DOM-LICENSE \
  React-Router-LICENSE \
  i18next-LICENSE \
  react-i18next-LICENSE \
  zustand-LICENSE \
  Motion-LICENSE; do
  test -s "${LICENSE_STAGE}/${license_file}"
done

echo "[7/9] Assemble allowlisted Windows and Docker deployment bundles"
mkdir -p "${WINDOWS_DIR}/licenses" "${WINDOWS_DIR}/secrets"
cp -a "${LICENSE_STAGE}/." "${WINDOWS_DIR}/licenses/"
cp "${ROOT_DIR}/run-viewer.ps1" "${WINDOWS_DIR}/"
cp "${ROOT_DIR}/run-remote-test.ps1" "${WINDOWS_DIR}/"
cp "${ROOT_DIR}/run-mock.ps1" "${WINDOWS_DIR}/"
cp "${ROOT_DIR}/README.md" "${WINDOWS_DIR}/"
cp "${ROOT_DIR}/LICENSE" "${ROOT_DIR}/CONTRIBUTING.md" "${ROOT_DIR}/SECURITY.md" "${WINDOWS_DIR}/"
cp -a "${ROOT_DIR}/docs" "${WINDOWS_DIR}/docs"
cp "${ROOT_DIR}/access-guard-public-keys.example.json" "${WINDOWS_DIR}/"
cp "${VERSION_FILE}" "${WINDOWS_DIR}/VERSION"
printf '%s\n' \
  'run-remote-test.ps1 reads these files:' \
  '  remote_test_cpamp_admin_key.txt' \
  '  remote_test_viewer_session_secret.txt' \
  'Do not include real secret values in a release archive.' \
  > "${WINDOWS_DIR}/secrets/README.txt"

mkdir -p "${DEPLOYMENT_DIR}/licenses" "${DEPLOYMENT_DIR}/secrets"
cp -a "${LICENSE_STAGE}/." "${DEPLOYMENT_DIR}/licenses/"
cp "${ROOT_DIR}/docker-compose.viewer.yml" "${DEPLOYMENT_DIR}/"
cp "${ROOT_DIR}/docker-compose.acceptance.yml" "${DEPLOYMENT_DIR}/"
cp "${ROOT_DIR}/docker-compose.access-guard.yml" "${DEPLOYMENT_DIR}/"
cp "${ROOT_DIR}/docker-compose.access-guard.acceptance.yml" "${DEPLOYMENT_DIR}/"
cp "${ROOT_DIR}/access-guard-public-keys.example.json" "${DEPLOYMENT_DIR}/"
cp "${ROOT_DIR}/.env.example" "${DEPLOYMENT_DIR}/.env.example"
cp "${ROOT_DIR}/README.md" "${DEPLOYMENT_DIR}/"
cp "${ROOT_DIR}/compose.yaml" "${ROOT_DIR}/compose.sub2api.yaml" "${DEPLOYMENT_DIR}/"
cp "${ROOT_DIR}/LICENSE" "${ROOT_DIR}/CONTRIBUTING.md" "${ROOT_DIR}/SECURITY.md" "${DEPLOYMENT_DIR}/"
cp -a "${ROOT_DIR}/docs" "${DEPLOYMENT_DIR}/docs"
cp "${VERSION_FILE}" "${DEPLOYMENT_DIR}/VERSION"
cp "${ROOT_DIR}/start-remote-test.ps1" "${DEPLOYMENT_DIR}/"
cp "${ROOT_DIR}/启动真实数据测试.cmd" "${DEPLOYMENT_DIR}/"
cp "${IMAGE_ARCHIVE}" "${DEPLOYMENT_DIR}/"
cp "${IMAGE_ARCHIVE}.sha256" "${DEPLOYMENT_DIR}/"
printf '%s\n' \
  'Create these files locally; never place real values in a release archive:' \
  '  cpamp_admin_key.txt' \
  '  viewer_session_secret.txt' \
  'Optional Sub2API overlay additionally reads:' \
  '  sub2api_admin_api_key.txt' \
  'Optional Access Guard overlay additionally reads:' \
  '  access_guard_management_key.txt' \
  '  access_guard_public_keys.json (copy and edit the empty example)' \
  '' \
  'On a rootful Linux Docker host, make them readable by the image nonroot group:' \
  '  chown root:65532 *.txt' \
  '  chmod 0640 *.txt' \
  > "${DEPLOYMENT_DIR}/secrets/README.txt"

write_tree_checksums() {
  local directory="$1"
  (
    cd "${directory}"
    while IFS= read -r -d '' path; do
      sha256sum "${path}"
    done < <(find . -type f ! -name SHA256SUMS -print0 | sort -z)
  ) > "${directory}/SHA256SUMS"
}

create_normalized_archive() {
  local source_directory="$1"
  local archive_path="$2"
  local parent_directory
  local base_name
  parent_directory="$(dirname "${source_directory}")"
  base_name="$(basename "${source_directory}")"
  tar --sort=name \
    --mtime="@${SOURCE_DATE_EPOCH}" \
    --owner=0 \
    --group=0 \
    --numeric-owner \
    --mode='u+rwX,go+rX,go-w' \
    -C "${parent_directory}" \
    -cf - "${base_name}" | gzip -n -9 > "${archive_path}"
}

write_tree_checksums "${WINDOWS_DIR}"
write_tree_checksums "${DEPLOYMENT_DIR}"

echo "[8/9] Create normalized archives and complete SHA-256 manifests"
create_normalized_archive "${WINDOWS_DIR}" "${WINDOWS_ARCHIVE}"
create_normalized_archive "${DEPLOYMENT_DIR}" "${DEPLOYMENT_ARCHIVE}"
(
  cd "${OUT_DIR}"
  sha256sum "$(basename "${WINDOWS_ARCHIVE}")" > "$(basename "${WINDOWS_ARCHIVE}").sha256"
  sha256sum "$(basename "${DEPLOYMENT_ARCHIVE}")" > "$(basename "${DEPLOYMENT_ARCHIVE}").sha256"
  while IFS= read -r -d '' path; do
    sha256sum "${path}"
  done < <(find "${WINDOWS_BASENAME}" -type f -print0 | sort -z) \
    > "cpamp-viewer_${VERSION}_windows_amd64.sha256"
  sha256sum \
    "${IMAGE_ARCHIVE_NAME}" \
    "$(basename "${WINDOWS_ARCHIVE}")" \
    "$(basename "${DEPLOYMENT_ARCHIVE}")" \
    "${IMAGE_ARCHIVE_NAME}.sha256" \
    "$(basename "${WINDOWS_ARCHIVE}").sha256" \
    "$(basename "${DEPLOYMENT_ARCHIVE}").sha256" \
    > SHA256SUMS
)
(
  cd "${WINDOWS_DIR}"
  sha256sum -c SHA256SUMS
)
(
  cd "${DEPLOYMENT_DIR}"
  sha256sum -c SHA256SUMS
)
(
  cd "${OUT_DIR}"
  sha256sum -c SHA256SUMS
)

echo "[9/9] Publish compatibility copies at the repository root"
cp "${WINDOWS_DIR}/cpamp-viewer.exe" "${ROOT_DIR}/cpamp-viewer.exe"
cp "${WINDOWS_DIR}/cpamp-viewer-remote-test.exe" "${ROOT_DIR}/cpamp-viewer-remote-test.exe"
cp "${WINDOWS_DIR}/cpamp-viewer-healthcheck.exe" "${ROOT_DIR}/cpamp-viewer-healthcheck.exe"
cp "${WINDOWS_DIR}/mock-cpamp.exe" "${ROOT_DIR}/mock-cpamp.exe"
cp "${IMAGE_ARCHIVE}" "${IMAGE_ARCHIVE}.sha256" "${ROOT_DIR}/"
cp "${WINDOWS_ARCHIVE}" "${WINDOWS_ARCHIVE}.sha256" "${ROOT_DIR}/"
cp "${DEPLOYMENT_ARCHIVE}" "${DEPLOYMENT_ARCHIVE}.sha256" "${ROOT_DIR}/"
(
  cd "${ROOT_DIR}"
  sha256sum \
    cpamp-viewer.exe \
    cpamp-viewer-remote-test.exe \
    cpamp-viewer-healthcheck.exe \
    mock-cpamp.exe \
    > "cpamp-viewer_${VERSION}_windows_amd64.sha256"
)

echo "Release artifacts written to ${OUT_DIR}"
