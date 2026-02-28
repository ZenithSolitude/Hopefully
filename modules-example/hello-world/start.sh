#!/bin/bash
# Простейший HTTP-модуль на bash + netcat
# Hopefully передаёт PORT, MODULE_NAME, MODULE_DIR, DATA_DIR
PORT="${PORT:-9100}"
MODULE_NAME="${MODULE_NAME:-hello-world}"

echo "[${MODULE_NAME}] Starting on port ${PORT}"

# Создаём данные модуля
mkdir -p "${DATA_DIR:-/tmp/hello-world}"

# Простой HTTP-сервер на bash + nc (если есть)
# Для production — замените на реальный бинарник (Go, Python, Node, etc.)
while true; do
    BODY="<!DOCTYPE html>
<html lang='ru' data-theme='dark'>
<head>
<meta charset='utf-8'>
<title>Hello World Module</title>
<link rel='stylesheet' href='https://cdn.jsdelivr.net/npm/daisyui@4.12.10/dist/full.min.css'>
<script src='https://cdn.tailwindcss.com'></script>
</head>
<body class='bg-base-300 min-h-screen flex items-center justify-center'>
<div class='card bg-base-200 shadow-xl w-96'>
  <div class='card-body text-center'>
    <div class='text-6xl mb-4'>👋</div>
    <h1 class='text-2xl font-bold'>Hello from module!</h1>
    <p class='opacity-60 text-sm'>Модуль: <code>${MODULE_NAME}</code></p>
    <p class='opacity-40 text-xs'>Порт: ${PORT} | Данные: ${DATA_DIR}</p>
    <p class='opacity-40 text-xs mt-2'>$(date)</p>
  </div>
</div>
</body>
</html>"

    LENGTH=${#BODY}
    RESPONSE="HTTP/1.1 200 OK\r\nContent-Type: text/html; charset=utf-8\r\nContent-Length: ${LENGTH}\r\nConnection: close\r\n\r\n${BODY}"

    if command -v nc &>/dev/null; then
        echo -e "$RESPONSE" | nc -l -p "$PORT" -q 1 2>/dev/null
    elif command -v python3 &>/dev/null; then
        # Fallback: Python one-liner
        python3 -c "
import http.server, os
os.chdir('${MODULE_DIR:-/tmp}')
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        body = b'''${BODY}'''
        self.send_response(200)
        self.send_header('Content-Type','text/html; charset=utf-8')
        self.send_header('Content-Length',len(body))
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, *a): pass
http.server.HTTPServer(('127.0.0.1', ${PORT}), H).serve_forever()
" &
        wait
        break
    else
        echo "[${MODULE_NAME}] ERROR: neither nc nor python3 found"
        sleep 60
    fi
done
