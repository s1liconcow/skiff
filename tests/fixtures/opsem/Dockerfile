FROM golang:1.26-alpine AS build
WORKDIR /src
COPY tests/fixtures/opsem/main.go .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/skiff-opsem main.go

FROM scratch
COPY --from=build /out/skiff-opsem /skiff-opsem
EXPOSE 8080
ENTRYPOINT ["/skiff-opsem"]
