# syntax=docker/dockerfile:1

# Stage 1: build the React bundle into internal/webui/dist (see web/vite.config.ts).
FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json web/.npmrc ./
RUN npm ci
COPY web/ ./
# vite writes to ../internal/webui/dist and stamps the source hash next to it.
RUN mkdir -p /src/internal/webui && npm run build

# Stage 2: the Go binary with that fresh bundle embedded. Pure Go (modernc SQLite), no cgo.
# Pinned by digest: a floating tag is what left the base frozen at EOL 1.24 (#36). Bump per deploy/README.md;
# scripts/check_go_base.py (CI) fails if this is outside the supported Go window or older than go.mod.
FROM golang:1.27.3-alpine@sha256:d530cf60723abf1ffae0cd16bb546b379ec0550d2f3815e5b16ed69dbc00fba4 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/webui/dist ./internal/webui/dist
# The commit, for the diagnostics view (GET /api/version); .git is not in the build context.
ARG REVISION=unknown
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X github.com/raufimusaddiq/antares/internal/server.Revision=${REVISION}" -o /out/antares ./cmd/antares \
 && mkdir /out/data

# Stage 3: no shell, no package manager, no toolchain, non-root.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/antares /antares
# The SQLite file lives on a volume mounted here; owned by the nonroot uid (65532).
COPY --from=build --chown=65532:65532 /out/data /data
ENV ANTARES_ADDR=:8080 ANTARES_DB_PATH=/data/antares.sqlite
USER 65532:65532
EXPOSE 8080
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 CMD ["/antares", "healthcheck"]
ENTRYPOINT ["/antares"]
