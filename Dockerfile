FROM golang:1.25 AS builder

WORKDIR /app

COPY . .

RUN go mod download

RUN CGO_ENABLED=0 go build -v -o alertsnitch \
    -ldflags="-X gitlab.com/yakshaving.art/alertsnitch/version.Version=$(git describe --tags --abbrev=0) \
              -X gitlab.com/yakshaving.art/alertsnitch/version.Date=$(date +%FT%T%z) \
              -X gitlab.com/yakshaving.art/alertsnitch/version.Commit=$(git rev-parse HEAD)"

# Final stage
FROM scratch

COPY ./database /database/

COPY --from=builder /app/alertsnitch /alertsnitch

EXPOSE 9567

ENTRYPOINT ["/alertsnitch"]
