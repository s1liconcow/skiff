FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY examples/stateful/postgres-ha ./examples/stateful/postgres-ha
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/postgres-ha ./examples/stateful/postgres-ha

FROM postgres:16-alpine
ENV POSTGRES_HOST_AUTH_METHOD=trust \
    POSTGRES_USER=postgres \
    POSTGRES_DB=postgres
COPY --from=build /out/postgres-ha /usr/local/bin/postgres-ha
USER 0
EXPOSE 5432 8008
ENTRYPOINT ["/usr/local/bin/postgres-ha"]
