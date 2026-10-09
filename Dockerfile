FROM node:22-alpine AS web-build
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS api-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-build /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/orp-nginx-importer ./cmd/server

FROM alpine:3.22
RUN apk add --no-cache ca-certificates \
 && addgroup -S importer \
 && adduser -S -D -H -G importer importer \
 && mkdir -p /app/web /data \
 && chown -R importer:importer /app /data
WORKDIR /app
COPY --from=api-build --chown=importer:importer /out/orp-nginx-importer ./orp-nginx-importer
COPY --from=api-build --chown=importer:importer /src/web/dist ./web/dist
USER importer
ENV NGINX_IMPORTER_ADDR=:8090
ENV NGINX_IMPORTER_STORAGE=/data
ENV ORP_BACKEND_URL=http://127.0.0.1:8081
EXPOSE 8090
ENTRYPOINT ["./orp-nginx-importer"]
