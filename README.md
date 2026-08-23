# Hostix

Hostix — кроссплатформенная CLI-утилита для запуска проектов в изолированной
среде. На macOS она использует Tart с fallback на Docker, а на Linux и Windows
— Docker.

Проект находится на раннем этапе разработки. Сейчас доступна диагностика
окружения:

```bash
go run ./cmd/hostix doctor
```

Команда сообщает, установлены ли Docker и Tart, показывает их версии и
выбираемый runtime. Полная команда `hostix run .` будет реализована следующим
вертикальным срезом.

## Разработка

Требуется Go 1.24 или новее.

```bash
make check
make build
./bin/hostix doctor
```

Архитектура описана в [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md), а полный
план — в [`PROJECT_PLAN.md`](PROJECT_PLAN.md).
