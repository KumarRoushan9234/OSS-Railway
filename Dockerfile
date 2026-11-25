# Panel image: Go binary + dashboard + docker CLI, buildx, git and railpack.
FROM node:24-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
COPY --from=web /web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /oss-railway .

FROM alpine:3.22
ARG RAILPACK_VERSION=0.43.0
RUN apk add --no-cache docker-cli docker-cli-buildx git ca-certificates tzdata \
 && case "$(uname -m)" in x86_64) a=x86_64 ;; aarch64) a=arm64 ;; *) echo unsupported; exit 1 ;; esac \
 && f="railpack-v${RAILPACK_VERSION}-${a}-unknown-linux-musl.tar.gz" \
 && cd /tmp \
 && wget -q "https://github.com/railwayapp/railpack/releases/download/v${RAILPACK_VERSION}/${f}" \
 && wget -q "https://github.com/railwayapp/railpack/releases/download/v${RAILPACK_VERSION}/checksums.txt" \
 && grep " ${f}\$" checksums.txt | sha256sum -c - \
 && tar -xzf "${f}" -C /usr/local/bin railpack \
 && rm -f /tmp/* \
 && railpack --version
COPY --from=go /oss-railway /usr/local/bin/oss-railway
ENV OSSR_DATA=/data
VOLUME /data
EXPOSE 3000
ENTRYPOINT ["oss-railway"]
