# Web deployment image. The desktop client is unchanged and is not what this image runs.
FROM golang:1.27.1-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/jstv-player .

FROM node:24-bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates gosu \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/jstv-player /usr/local/bin/jstv-player
COPY docker/entrypoint.sh /usr/local/bin/jstv-entrypoint
RUN chmod 0755 /usr/local/bin/jstv-entrypoint /usr/local/bin/jstv-player \
    && mkdir -p /data \
    && chown node:node /data
ENV JSTV_DATA_DIR=/data \
    JSTV_WEB_ADDR=:8080
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["jstv-entrypoint"]
