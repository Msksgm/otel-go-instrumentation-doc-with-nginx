#!/bin/sh
set -e

# 環境変数がセットされていない場合はデフォルト値を使用
: ${OTEL_EXPORTER_ENDPOINT:="host.docker.internal:4317"}

echo "Using OTLP endpoint: ${OTEL_EXPORTER_ENDPOINT}"
envsubst '${OTEL_EXPORTER_ENDPOINT}' </etc/nginx/nginx.conf.template >/etc/nginx/nginx.conf

# Nginx を起動
exec nginx -g 'daemon off;'
