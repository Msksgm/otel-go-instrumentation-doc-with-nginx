#!/bin/sh
set -e

# 環境変数を nginx.conf.template から nginx.conf に展開
envsubst '${OTLP_ENDPOINT}' </etc/nginx/nginx.conf.template >/etc/nginx/nginx.conf

# Nginx を起動
exec nginx -g 'daemon off;'

