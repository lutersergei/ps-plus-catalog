# --- build stage ---
FROM golang:1.25 AS build
WORKDIR /src

# Сначала зависимости (кэшируется, пока go.mod/go.sum не меняются)
COPY go.mod go.sum ./
RUN go mod download

# Исходники (шаблон index.html встраивается через go:embed)
COPY . .
# Чистый Go SQLite (modernc) → CGO не нужен, бинарь статический
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/ps-extra .

# --- runtime stage ---
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build --chown=65532:65532 /out/ps-extra /usr/local/bin/ps-extra
# /data — рабочая папка: сюда ложится ps-extra.db и (опционально) .env.
# Том должен быть доступен на запись UID 65532: mkdir -p data && chown 65532:65532 data
WORKDIR /data
USER 65532:65532

EXPOSE 8080
ENTRYPOINT ["ps-extra"]
# По умолчанию — веб-сервер; БД берётся из /data (том). Для сбора данных:
#   docker run ... ps-extra sync
CMD ["serve", "-addr", ":8080", "-db", "/data/ps-extra.db"]
