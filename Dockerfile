# syntax=docker/dockerfile:1.7

FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/aionfs-devd ./cmd/aionfs-devd

FROM gcr.io/distroless/base-debian12:nonroot
WORKDIR /srv/aionfs
COPY --from=build /out/aionfs-devd /usr/local/bin/aionfs-devd
ENTRYPOINT ["/usr/local/bin/aionfs-devd"]
CMD ["-listen", "0.0.0.0:7080", "-data-dir", "/srv/aionfs/data"]
