# Hello World — пример модуля Hopefully

Минимальный пример модуля-HTTP-сервера на bash.

## Структура модуля

```
manifest.json   ← обязательно: метаданные
start.sh        ← entrypoint: запускается при активации
install.sh      ← опционально: запускается при установке
README.md       ← документация
```

## manifest.json — поля

| Поле | Тип | Описание |
|------|-----|----------|
| `name` | string | Уникальное имя `[a-z0-9_-]` |
| `version` | string | Версия (semver) |
| `description` | string | Описание |
| `author` | string | Автор |
| `entrypoint` | string | Исполняемый файл/скрипт (относительно корня модуля) |
| `port` | int | Если модуль — HTTP-сервер, его порт (127.0.0.1 only) |
| `menu_icon` | string | Emoji для меню |
| `menu_label` | string | Название в меню |
| `menu_pos` | int | Позиция в меню (меньше = выше) |

## Переменные окружения

Hopefully передаёт в запускаемый процесс:

| Переменная | Описание |
|------------|----------|
| `MODULE_NAME` | Имя модуля |
| `MODULE_DIR` | Директория модуля на диске |
| `DATA_DIR` | Директория для данных модуля |
| `PORT` | Порт из manifest.json |

## Примеры модулей на разных языках

**Python:**
```bash
#!/usr/bin/env python3
# entrypoint: "start.py"
import http.server, os
PORT = int(os.environ.get('PORT', 8000))
# ... ваш код
```

**Node.js:**
```javascript
// entrypoint: "start.js"
const http = require('http');
const port = process.env.PORT || 3000;
http.createServer((req, res) => {
  res.end('<h1>Hello!</h1>');
}).listen(port, '127.0.0.1');
```

**Go (бинарник):**
```json
{ "entrypoint": "server" }
```
Скомпилированный бинарник `server` в корне модуля.

## Установка без HTTP-сервера

Если у модуля нет `port` — Hopefully ищет `web/index.html` и отдаёт как статику.
Если ни того ни другого — показывает заглушку.
