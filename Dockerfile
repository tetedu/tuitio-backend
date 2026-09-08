FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/tuitio-api ./cmd/api

FROM alpine:3.20
RUN adduser -D app
USER app
COPY --from=build /out/tuitio-api /usr/local/bin/tuitio-api
EXPOSE 8080
ENTRYPOINT ["tuitio-api"]
