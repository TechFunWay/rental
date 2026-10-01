#!/bin/bash
# Linux 发布二进制冒烟测试（在 WSL 内执行）
set -e
cd /mnt/f/workspace/techfunway/gitea/rental/server
./rental-linux-amd64 -data-dir /tmp/rental-smoke -web-dir ./static/dist -port 18911 > /tmp/rental-smoke.log 2>&1 &
SRV=$!
trap "kill $SRV 2>/dev/null; rm -rf /tmp/rental-smoke" EXIT
for i in $(seq 1 25); do
  curl -s --max-time 2 http://127.0.0.1:18911/api/health >/dev/null 2>&1 && break
  sleep 0.4
done
echo "version: $(curl -s http://127.0.0.1:18911/api/version)"
curl -s -o /dev/null -w "index: %{http_code}\n" http://127.0.0.1:18911/
REG=$(curl -s -X POST http://127.0.0.1:18911/api/auth/register -H 'Content-Type: application/json' -d '{"username":"smoke","password":"smoke12345"}')
echo "register: $(echo "$REG" | head -c 60)..."
ROOM=$(curl -s -X POST http://127.0.0.1:18911/api/rental/rooms -H "Authorization: Bearer $(echo "$REG" | grep -o '"token":"[^"]*"' | cut -d'"' -f4)" -H 'Content-Type: application/json' -d '{"room_no":"1"}')
echo "room: $(echo "$ROOM" | head -c 80)"
echo "SMOKE-OK"
