FROM golang:1.24 AS builder

WORKDIR /app

COPY go.mod go.sum ./

RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o /huawei_exporter

FROM scratch

COPY --from=builder /huawei_exporter /huawei_exporter

WORKDIR /

ENTRYPOINT ["/huawei_exporter"]
