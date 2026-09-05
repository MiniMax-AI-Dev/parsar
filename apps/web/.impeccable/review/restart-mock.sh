#!/usr/bin/env bash
# Restart the fixture mock API on 18080 (safe to call from several agents;
# whoever restarts last wins, and every fixtures/*.py file is loaded).
set -u
cd "$(dirname "$0")"
pkill -f "review/mock-api.py" 2>/dev/null || true
pkill -f "parsar-mock-api.py" 2>/dev/null || true
pkill -f "mock-api.py 18080" 2>/dev/null || true
sleep 0.5
nohup python3 mock-api.py 18080 > /tmp/parsar-mock.log 2>&1 &
sleep 1
curl --noproxy '*' -sS -m 5 -o /dev/null -w "mock: %{http_code}\n" http://127.0.0.1:18080/api/v1/me
grep -E "loaded|FAILED" /tmp/parsar-mock.log || true
