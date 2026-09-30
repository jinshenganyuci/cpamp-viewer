# syntax=docker/dockerfile:1.7
ARG VERSION=2.6.0
ARG SOURCE_DATE_EPOCH=0

FROM node:24-alpine@sha256:a0b9bf06e4e6193cf7a0f58816cc935ff8c2a908f81e6f1a95432d679c54fbfd AS web
ARG VERSION
WORKDIR /src/web-cpamp
COPY web-cpamp/package*.json ./
RUN npm ci --ignore-scripts --no-audit --no-fund
COPY web-cpamp/ ./
RUN VERSION="${VERSION}" npm run build

FROM golang:1.26-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS server
WORKDIR /src/server
COPY server/go.mod ./
COPY server/ ./
COPY --from=web /src/server/webdist ./webdist
RUN CGO_ENABLED=0 go build -buildvcs=false -mod=readonly -trimpath -ldflags="-s -w" -o /out/cpamp-viewer . && \
    CGO_ENABLED=0 go build -buildvcs=false -mod=readonly -trimpath -ldflags="-s -w" -o /out/cpamp-viewer-healthcheck ./cmd/healthcheck

FROM gcr.io/distroless/static-debian12:nonroot@sha256:b7bb25d9f7c31d2bdd1982feb4dafcaf137703c7075dbe2febb41c24212b946f
ARG VERSION
LABEL org.opencontainers.image.title="CPAMP Viewer" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.description="Read-only CPA Manager Plus viewer with public Access Guard key quotas" \
      org.opencontainers.image.source="https://github.com/jinshenganyuci/cpamp-viewer" \
      org.opencontainers.image.licenses="MIT AND Apache-2.0 AND BSD-3-Clause"
WORKDIR /app
COPY LICENSE /licenses/CPAMP-Viewer-LICENSE
COPY --from=server /out/cpamp-viewer /app/cpamp-viewer
COPY --from=server /out/cpamp-viewer-healthcheck /app/cpamp-viewer-healthcheck
COPY --from=web /src/web-cpamp/UPSTREAM_LICENSE /licenses/CPA-Manager-Plus-LICENSE
COPY --from=web /src/web-cpamp/UPSTREAM.md /licenses/CPA-Manager-Plus-UPSTREAM.md
COPY --from=web /src/web-cpamp/node_modules/echarts/LICENSE /licenses/ECharts-LICENSE
COPY --from=web /src/web-cpamp/node_modules/echarts/NOTICE /licenses/ECharts-NOTICE
COPY --from=web /src/web-cpamp/node_modules/zrender/LICENSE /licenses/zrender-LICENSE
COPY --from=web /src/web-cpamp/node_modules/react/LICENSE /licenses/React-LICENSE
COPY --from=web /src/web-cpamp/node_modules/react-dom/LICENSE /licenses/React-DOM-LICENSE
COPY --from=web /src/web-cpamp/node_modules/react-router-dom/LICENSE.md /licenses/React-Router-LICENSE
COPY --from=web /src/web-cpamp/node_modules/i18next/LICENSE /licenses/i18next-LICENSE
COPY --from=web /src/web-cpamp/node_modules/react-i18next/LICENSE /licenses/react-i18next-LICENSE
COPY --from=web /src/web-cpamp/node_modules/zustand/LICENSE /licenses/zustand-LICENSE
COPY --from=web /src/web-cpamp/node_modules/motion/LICENSE.md /licenses/Motion-LICENSE
EXPOSE 18417
USER nonroot:nonroot
ENTRYPOINT ["/app/cpamp-viewer"]
