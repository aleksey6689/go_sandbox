# ---------- build stage ----------
FROM golang:1.23-alpine AS builder
WORKDIR /src
ENV CGO_ENABLED=0

# Копируем только go.mod (go.sum может не существовать)
COPY go.mod ./
RUN go mod download

# Теперь копируем весь проект
COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /bin/server ./...

# ---------- runtime stage ----------
FROM gcr.io/distroless/static:nonroot
WORKDIR /app
COPY --from=builder /bin/server /app/server
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/app/server"]
