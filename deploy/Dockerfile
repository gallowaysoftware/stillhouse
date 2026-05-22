# syntax=docker/dockerfile:1.7

# ----- Stage 1: build the web frontend ---------------------------------------
FROM docker.io/library/node:20-alpine AS web-builder
WORKDIR /src/web

COPY web/package.json web/package-lock.json* ./
RUN npm ci

# Need the .proto files + buf config to regenerate the TS client during build.
COPY proto /src/proto
COPY web/ /src/web/

# Regenerate the TS client from the source-of-truth .proto so the built image
# can't drift from committed generated code.
RUN npx -y -p @bufbuild/buf@latest -- buf generate /src/proto --template /src/proto/buf.gen.web.yaml
RUN npm run build

# ----- Stage 2: build the Go backend -----------------------------------------
FROM docker.io/library/golang:1.24-alpine AS go-builder
RUN apk add --no-cache git ca-certificates
WORKDIR /src

COPY backend/go.mod backend/go.sum ./backend/
RUN cd backend && go mod download

COPY proto /src/proto
COPY backend /src/backend

# Generated Go code is committed, so no buf generate step needed here.
RUN cd backend && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags='-s -w' -o /out/stillhouse-server ./cmd/server && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags='-s -w' -o /out/stillhouse-seed   ./cmd/seed

# ----- Stage 3: minimal runtime ----------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app

COPY --from=go-builder /out/stillhouse-server /app/stillhouse-server
COPY --from=go-builder /out/stillhouse-seed   /app/stillhouse-seed
COPY --from=web-builder /src/web/dist         /app/static

ENV STILLHOUSE_STATIC_DIR=/app/static
ENV STILLHOUSE_ADDR=:8080
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/stillhouse-server"]
