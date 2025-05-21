FROM golang:1.24 AS build

WORKDIR /go/src/practice-4
COPY . .

RUN go test ./...
ENV CGO_ENABLED=0
RUN go install ./cmd/...

# ==== Final image ====
FROM alpine:latest
WORKDIR /opt/practice-4

# Копируем скрипт и делаем его исполняемым
COPY entry.sh /opt/practice-4/
RUN chmod +x /opt/practice-4/entry.sh

# Копируем собранные бинарники
COPY --from=build /go/bin/* /opt/practice-4/

# Для отладки (можно убрать позже)
RUN ls -l /opt/practice-4

ENTRYPOINT ["/opt/practice-4/entry.sh"]
CMD ["server"]
