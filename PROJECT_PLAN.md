# Hostix — CLI-утилита для деплоя, запуска и оптимизации контейнеров (Tart/Docker)

> Рабочее название: **Hostix** (замени на своё). Ниже — полный пакет документации для старта разработки: видение, архитектура, тех-стек, роадмап, структура репо, спецификация команд и типовые риски.

---

## 1. Видение продукта

Единая CLI-утилита, которая абстрагирует запуск кода в изолированной среде независимо от ОС:

- **macOS** → использует **Tart** (VM на Apple Virtualization Framework)
- **Linux/Windows** → использует **Docker**

Утилита определяет стек проекта (Python / C# / Go / PHP), собирает подходящий образ, запускает его и даёт единый интерфейс управления (exec, logs, status, stop) независимо от того, что под капотом — VM или контейнер.

**Ключевая ценность:** разработчик пишет одну команду (`hostix run .`), а дальше не думает, Docker у него или Tart.

---

## 2.核心 принципы архитектуры

1. **Runtime-агностичность** — вся бизнес-логика работает через интерфейс `Runtime`, конкретные реализации (`DockerRuntime`, `TartRuntime`) — это адаптеры.
2. **Явное разделение горячих и холодных путей** — Go отвечает за оркестрацию/CLI/IO, C++ подключается только туда, где профилирование показало реальный bottleneck (агрегация метрик, парсинг больших логов).
3. **Auto-detect по умолчанию, explicit-override по флагу** — стек определяется по файлам проекта, но всегда можно переопределить.
4. **Не притворяться, что Tart и Docker одинаковые** — разное время старта, разный формат образов, разный exec-механизм (SSH vs docker exec). Абстракция прячет это для пользователя, но не для внутренней логики.
5. **Idempotent CLI** — повторный `hostix run` не плодит дубликаты, а переиспользует/пересоздаёт по стратегии.

---

## 3. Тех-стек

### 3.1 Основной язык — Go
- **Cobra** — CLI-фреймворк (команды, флаги, автодополнение)
- **Viper** — конфиги (`hostix.yaml`, env-переменные)
- **docker/docker/client** — официальный Docker SDK (вместо шелл-вызовов)
- **golang.org/x/crypto/ssh** — SSH-клиент для exec в Tart VM
- **zerolog / zap** — структурированное логирование
- **cgo** — мост к C++ модулю

### 3.2 Производительность — C++
- Модуль собирается как статическая библиотека, линкуется через cgo
- Зоны ответственности:
  - агрегация метрик (CPU/mem/IO) с высокой частотой поллинга
  - быстрый парсинг/фильтрация больших логов
  - при необходимости — кастомный in-VM supervisor/health-checker
- **Важно:** не начинать с C++. Сначала MVP полностью на Go, C++ подключать по факту профилирования (`pprof`), когда появится измеримый bottleneck.

### 3.3 Runtime-бэкенды
| ОС | Runtime | Механизм exec | Формат образа |
|---|---|---|---|
| macOS | Tart | SSH | Tart VM image (OCI-based) |
| Linux/Windows | Docker | `docker exec` / SDK Attach | Dockerfile → образ |

### 3.4 Поддерживаемые стеки приложений
| Стек | Базовый образ (Docker) | Особенности |
|---|---|---|
| Python | `python:3.12-slim` | auto-detect `requirements.txt` / `pyproject.toml`, автозапуск через `uvicorn`/`gunicorn` при обнаружении FastAPI/Flask/Django |
| C# | `mcr.microsoft.com/dotnet/aspnet` (runtime) + `sdk` (build stage) | auto-detect `*.csproj`, multi-stage build |
| Go | `golang:alpine` → `gcr.io/distroless/static` | multi-stage, статическая сборка |
| PHP | `php:8.3-fpm` + `nginx` (или `php:8.3-cli` для простых скриптов) | auto-detect `composer.json` |

### 3.5 Вспомогательное
- **GitHub Actions** — CI (тесты Go, сборка C++ модуля под разные ОС, линт)
- **GoReleaser** — сборка и публикация бинарников (macOS arm64/amd64, Linux, Windows)
- **Testcontainers-go** — интеграционные тесты DockerRuntime
- **golangci-lint** — линтинг

---

## 4. Архитектура репозитория

```
hostix/
├── cmd/
│   └── hostix/
│       └── main.go                 # entrypoint
├── internal/
│   ├── cli/                        # cobra-команды
│   │   ├── deploy.go
│   │   ├── run.go
│   │   ├── exec.go
│   │   ├── logs.go
│   │   ├── status.go
│   │   └── stop.go
│   ├── runtime/                    # Runtime Abstraction Layer
│   │   ├── runtime.go              # interface Runtime
│   │   ├── docker/
│   │   │   └── docker_runtime.go
│   │   ├── tart/
│   │   │   └── tart_runtime.go
│   │   └── selector.go             # выбор runtime по ОС/наличию
│   ├── detect/                     # auto-detect стека проекта
│   │   ├── detect.go
│   │   ├── python.go
│   │   ├── csharp.go
│   │   ├── golang.go
│   │   └── php.go
│   ├── image/                      # генерация Dockerfile/Tart-образов
│   │   └── templates/
│   │       ├── python.Dockerfile.tmpl
│   │       ├── csharp.Dockerfile.tmpl
│   │       ├── go.Dockerfile.tmpl
│   │       └── php.Dockerfile.tmpl
│   ├── config/                     # hostix.yaml парсинг (viper)
│   │   └── config.go
│   └── metrics/                    # cgo-мост к C++ модулю
│       ├── metrics.go
│       └── cgo_bridge.cc
├── native/                         # C++ модуль
│   ├── CMakeLists.txt
│   ├── include/
│   └── src/
│       ├── metrics_collector.cpp
│       └── log_parser.cpp
├── pkg/                            # публичные пакеты (если нужен SDK)
├── test/
│   ├── integration/
│   └── e2e/
├── docs/
│   ├── ARCHITECTURE.md
│   ├── CLI_SPEC.md
│   └── ADR/                        # Architecture Decision Records
├── .github/workflows/
│   ├── ci.yml
│   └── release.yml
├── Makefile
├── go.mod
└── README.md
```

---

## 5. Спецификация CLI (v1)

```bash
hostix run .                     # auto-detect стека, создать+запустить контейнер
hostix run . --stack python      # явно указать стек
hostix run . --runtime docker    # форсировать runtime (игнорируя ОС-дефолт)

hostix deploy .                  # run + persistent-режим (restart policy, детач)

hostix ps                        # список запущенных контейнеров/VM hostix
hostix status <id>               # детальный статус (CPU/mem, uptime, порты)
hostix logs <id> [--follow]      # логи (follow-режим — стрим)
hostix exec <id> -- <cmd>        # выполнить команду внутри

hostix stop <id>
hostix rm <id>                   # удалить (с confirm, если запущен)

hostix config init               # сгенерировать hostix.yaml
hostix doctor                    # проверка окружения (Docker/Tart установлены? версии?)
```

### Пример `hostix.yaml`
```yaml
stack: python
runtime: auto        # auto | docker | tart
source: .
env:
  PORT: "8000"
ports:
  - "8000:8000"
resources:
  cpu: 2
  memory: 512Mi
restart_policy: on-failure
```

---

## 6. Роадмап

### Фаза 0 — Подготовка (1 неделя)
- [ ] Зафиксировать имя проекта, лицензию, репозиторий
- [ ] Настроить `go.mod`, базовый CI (lint + test на пустом проекте)
- [ ] Написать `ARCHITECTURE.md` и первый ADR (выбор Go+cgo+C++ вместо чистого Go)
- [ ] `hostix doctor` — детект наличия Docker/Tart в системе (это нужно раньше всего остального)

### Фаза 1 — MVP на Docker, только Go (3-4 недели)
Цель: `hostix run .` работает для Python-проекта на Linux/macOS через Docker.
- [ ] `Runtime` интерфейс + `DockerRuntime` (через `docker/docker/client`)
- [ ] `detect` пакет: определение Python по `requirements.txt`/`pyproject.toml`
- [ ] Генерация Dockerfile из шаблона для Python
- [ ] Команды: `run`, `ps`, `logs`, `stop`, `rm`
- [ ] Базовые интеграционные тесты (testcontainers-go)
- [ ] `hostix.yaml` — чтение конфига (viper)

**Критерий готовности:** можно склонировать любой FastAPI/Flask репозиторий и поднять его одной командой.

### Фаза 2 — Остальные стеки (2-3 недели)
- [ ] `detect` + Dockerfile-шаблоны для C#, Go, PHP
- [ ] `exec` команда (docker exec через SDK)
- [ ] `status` с базовыми метриками (через Docker Stats API, без C++ пока)
- [ ] Флаг `--stack` для override auto-detect

**Критерий готовности:** все 4 стека запускаются и управляются одинаковым CLI.

### Фаза 3 — TartRuntime (macOS-only) (3-4 недели)
Это самая рискованная фаза — Tart работает принципиально иначе (VM, не namespace).
- [ ] Исследовать Tart CLI/API, зафиксировать в ADR отличия от Docker-модели
- [ ] `TartRuntime`: Create/Start/Stop через вызов `tart` бинарника (`os/exec`)
- [ ] Exec через SSH (`golang.org/x/crypto/ssh`) — нужен provisioning SSH-ключей при создании VM
- [ ] Logs — либо через SSH-стрим файла лога, либо через serial console Tart
- [ ] `selector.go`: авто-выбор Tart на macOS с fallback на Docker, если Tart не установлен

**Критерий готовности:** `hostix run .` на чистом macOS без Docker поднимает VM через Tart и ведёт себя идентично по CLI-интерфейсу.

### Фаза 4 — Профилирование и C++ (2-3 недели, опционально/по необходимости)
- [ ] Профилировать Go-реализацию под нагрузкой (много контейнеров, частый polling метрик)
- [ ] Если узкое место подтверждено — вынести сбор/агрегацию метрик в C++ модуль через cgo
- [ ] Собрать cross-compilation pipeline для C++ части (macOS arm64/x86, Linux)

**Важно:** эту фазу можно пропустить полностью, если Go справляется — не делать ради самого факта наличия C++.

### Фаза 5 — Полировка и релиз (2 недели)
- [ ] `hostix doctor` — полноценная диагностика окружения
- [ ] Автодополнение shell (cobra completion)
- [ ] GoReleaser — сборка бинарников под все платформы
- [ ] Документация: README, quickstart, примеры для каждого стека
- [ ] `hostix deploy` — persistent-режим с restart policy

### Фаза 6 — Дальше (backlog, не MVP)
- Оркестрация нескольких контейнеров (docker-compose-like манифест)
- Web UI / TUI дашборд для статуса
- Registry — публикация собранных образов
- Remote hosts (не только localhost) — деплой на удалённый Docker daemon по SSH/TLS

---

## 7. Риски и как их закрывать

| Риск | Влияние | Митигация |
|---|---|---|
| Tart и Docker слишком разные, абстракция протекает | Высокое | Заложить в интерфейс `Runtime` только то, что реально общее (Create/Start/Stop/Exec/Logs/Status); специфику (VM image build, SSH provisioning) держать внутри адаптера, не тащить в общий контракт |
| C++/cgo усложняет сборку под все платформы раньше времени | Среднее | Не подключать C++, пока нет профилирования, показывающего реальный bottleneck; MVP — чистый Go |
| Auto-detect стека ошибается на монорепах / нестандартной структуре | Среднее | Всегда давать `--stack` override; `doctor`/`detect` команда с `--dry-run`, показывающая что определилось, до реального запуска |
| SSH-provisioning в Tart VM — источник хрупкости (ключи, таймауты старта VM) | Среднее | Retry с backoff на подключение по SSH после старта VM, чёткие таймауты и понятные ошибки |
| Docker SDK версионирование ломается между версиями Docker Engine | Низкое | Зафиксировать минимальную поддерживаемую версию Docker API в `doctor` |

---

## 8. Метрики успеха MVP

- `hostix run .` от `git clone` до работающего контейнера — **< 60 секунд** для Python/Go/PHP на Docker
- `hostix run .` на Tart — старт VM не должен восприниматься как "зависание" (нужен явный прогресс-индикатор, т.к. VM стартует дольше контейнера)
- Zero-config запуск для всех 4 стеков на типовых проектах (без ручного Dockerfile)

---

## 9. Что писать первым (порядок для "вайб-кодинга")

1. `internal/runtime/runtime.go` — интерфейс (весь проект строится вокруг него)
2. `internal/runtime/docker/docker_runtime.go` — первая реализация
3. `internal/detect/python.go` — самый простой детект для первого сквозного теста
4. `internal/cli/run.go` — команда, которая связывает всё выше
5. Дальше — по Фазам 1→5 из роадмапа выше

Такой порядок даёт рабочий вертикальный срез (end-to-end `hostix run .` для одного стека на Docker) максимально быстро, и дальше можно расширять вширь (стеки) и вглубь (Tart, метрики).
