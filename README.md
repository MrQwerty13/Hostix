# Hostix

Hostix — кроссплатформенная CLI-утилита для запуска проектов в изолированной
среде. Целевая архитектура использует Tart на macOS с fallback на Docker, а
Docker — на Linux и Windows. Текущий ранний срез запускает проекты через Docker
на всех платформах; TartRuntime будет добавлен отдельной фазой.

Проект находится на раннем этапе разработки. Сейчас доступны диагностика
окружения и первый вертикальный сценарий запуска Python-проектов через Docker:

```bash
go run ./cmd/hostix doctor
go run ./cmd/hostix run ./path/to/python-project
```

`run` определяет Python по `requirements.txt` или `pyproject.toml`, распознаёт
типовые FastAPI, Flask и Django entrypoint, генерирует Dockerfile во временной
сборочной области, собирает образ и запускает контейнер. Docker daemon должен
быть запущен.

Если безопасно определить команду запуска невозможно, укажите её после `--`:

```bash
go run ./cmd/hostix run . -- python main.py
```

Порты и переменные окружения можно передать флагами:

```bash
go run ./cmd/hostix run . --port 8080:8000 --env MODE=development
```

Повторный запуск использует стабильное имя и заменяет только контейнер,
помеченный как созданный Hostix. Контейнер другого владельца с таким же именем
не удаляется.

## Разработка

Требуется Go 1.24 или новее.

```bash
make check
make build
./bin/hostix doctor
```

Архитектура описана в [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md), а полный
план — в [`PROJECT_PLAN.md`](PROJECT_PLAN.md).
