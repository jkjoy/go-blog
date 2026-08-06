# syntax=docker/dockerfile:1

ARG GO_VERSION=1.25
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/go-blog ./cmd/blog

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 10001 blog \
    && adduser -S -D -H -u 10001 -G blog blog \
    && mkdir -p /app/data /app/uploads \
    && chown -R blog:blog /app

COPY --from=build /out/go-blog /usr/local/bin/go-blog

WORKDIR /app
ENV BLOG_LISTEN=0.0.0.0:8080 \
    BLOG_DATABASE_DRIVER=sqlite \
    BLOG_DATABASE_DSN=/app/data/blog.sqlite \
    BLOG_UPLOAD_DIR=/app/uploads

EXPOSE 8080
USER blog

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["go-blog"]
