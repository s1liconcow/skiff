FROM golang:1.24 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY examples/stacks/api-multiregion-database ./examples/stacks/api-multiregion-database
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/orders-rpc ./examples/stacks/api-multiregion-database

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/orders-rpc /orders-rpc

ENV PORT=8080
EXPOSE 8080

USER nonroot:nonroot
ENTRYPOINT ["/orders-rpc"]
