FROM golang:1.24-alpine AS build
WORKDIR /go/src/app
COPY go.mod ./
RUN go mod tidy # Додано для чистоти залежностей
RUN go mod download
RUN go mod verify

COPY . .

# Збираємо всі виконувані файли з директорії cmd
# Вони будуть розміщені в /go/bin/ з іменами директорій (db, server, lb)
ENV CGO_ENABLED=0
RUN go install ./cmd/...

FROM alpine:latest

# Встановлюємо робочу директорію для фінального образу
WORKDIR /opt/app

# Копіюємо entry.sh та робимо його виконуваним
COPY entry.sh /opt/app/
RUN chmod +x /opt/app/entry.sh

# Копіюємо всі зібрані бінарні файли з білдера
COPY --from=build /go/bin/* /opt/app/

# Створюємо директорію для даних БД та оголошуємо її як volume
RUN mkdir -p /opt/app/database_data
VOLUME /opt/app/database_data

# Визначаємо entrypoint
ENTRYPOINT ["/opt/app/entry.sh"]

# Команда за замовчуванням (може бути перевизначена в docker-compose.yml)
# Наприклад, для сервера, якщо він найчастіше використовується
CMD ["server"]