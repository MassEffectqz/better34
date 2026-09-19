# briefly (better34)

> **Self-hosted, offline-first LAN viewer for booru-style imageboards**
> (rule34 & friends). Go + Gin backend with a privacy-focused PWA frontend.

[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Docker](https://img.shields.io/badge/Docker-ready-2496ed?logo=docker)](Dockerfile)
[![PWA](https://img.shields.io/badge/PWA-ready-4285EF?logo=pwa)](static/js/sw.js)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](https://github.com/MassEffectqz/better34/pulls)

---

## ✨ Features

- **Self-hosted** — всё в локальной сети, без посторонних серверов.
- **Offline-first PWA** — просматривайте раньше скачанное без интернета, даже без вкладки.
- **Privacy-first** — ваш трафик и скачанные файлы никогда не уходят наружу.
- **Disk cache + pHash dedup** — не качаете одно и то же дважды.
- **Semantic search via Ollama** — ищите `?котики в шляпах`, а не только теги.
- **Blurhash previews** — мгновенная сетка, даже для больших картинок.
- **QR-login + remote sync** — вход по QR-коду и «пульт» для телефона/ПК.
- **HTTP/3 (QUIC)** — если браузер поддерживает.
- **User-defined providers** — добавляйте свои gelbooru-сайты через JSON.
- **Accounts & collections** — лайки, скрытия, коллекции, комментарии.

---

## 🚀 Quick Start

### 1. From source (`run.cmd`)

> **Requirements:** [Go ≥ 1.25](https://go.dev/dl/)

```bash
git clone --depth 1 https://github.com/MassEffectqz/better34.git
cd better34
copy .env.example .env    # (опционально, отредактируйте под себя)
go mod tidy
npm ci                  # только для разработки фронтенда
run.cmd                 # или: go run .
```

После запуска в консоли вы увидите свой LAN-IP — откройте в браузере:

```
https://<ваш-IP-в-LAN>:3000
```

> При первом входе браузер предупредит о самоподписном сертификате — нажмите **«Дополнительно → Перейти на сайт»**.

### 2. Docker (рекомендуется для постоянного сервера)

```bash
git clone https://github.com/MassEffectqz/better34.git
cd better34
cp docker-compose.yml.example docker-compose.yml
docker compose up -d
```

Откройте: `https://localhost:3000`

---

## ⚙️ Environment Variables

| Переменная                  | По умолч.          | Назначение                                      |
|----------------------------|------------------|-------------------------------------------------|
| `BRIEFLY_HOST`             | `0.0.0.0`        | Интерфейс сервера (`127.0.0.1` — локально)       |
| `BRIEFLY_PORT`             | `3000`           | Порт                                            |
| `BRIEFLY_TLS`              | `1`              | HTTPS + HTTP/2 (self-signed сертификат)        |
| `BRIEFLY_H3`               | `1`              | HTTP/3/QUIC (отключить: `0`)                    |
| `BRIEFLY_MEDIA_CACHE_GB`   | `20`             | Дисковый кэш медиа                              |
| `BRIEFLY_OLLAMA`           | `127.0.0.1:11434`| URL локальной Ollama                            |
| `BRIEFLY_OLLAMA_MODEL`     | `dolphin-mistral:7b` | Модель для семантического поиска            |
| `BRIEFLY_ALLOWED_HOSTS`    | авто             | Белый список хостов для доступа                 |
| `BRIEFLY_DEBUG`            | —                | `1` = unbundled frontend + лог запросов         |

Полный список → [.env.example](.env.example)

---

## 🛠 Development

```bash
npm run check        # eslint + tsc + frontend tests
npm run build        # пересборка static/js/dist/app.js после правок
go test ./...        # Go-тесты
go run .             # запуск сервера
```

---

## 📄 Documentation

- [Environment variables](.env.example)
- [Dockerfile build](Dockerfile)
- [Docker Compose setup](docker-compose.yml.example)
- [Architecture notes → Wiki](https://github.com/MassEffectqz/better34/wiki)

---

## 🤝 Contributing

PR-ы приветствуются! Перед тем как открывать PR — проверьте:

```bash
npm run check && go test ./...
```

---

## 📜 License

MIT © [better34 contributors](https://github.com/MassEffectqz/better34/graphs/contributors)

---

<!--
  ┌────────────────────────────────────────────────────────────┐
  │  Screenshots:                                                │
  │  Replace the lines below with real screenshots.  │
  │  Suggested sections:                                         │
  │  1) Feed grid with blurhash placeholders                    │
  │  2) Single post viewer                                      │
  │  3) Settings / QR-login                                     │
  │                                                              │
  │  ![Feed](docs/screenshot-feed.png)                          │
  │  ![Viewer](docs/screenshot-viewer.png)                      │
  │  ![Settings](docs/screenshot-settings.png)                  │
  └────────────────────────────────────────────────────────────┘
-->
