FROM golang:1.22-bookworm AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -o /out/igrec ./cmd/igrec

FROM debian:bookworm-slim
WORKDIR /app
COPY --from=build /out/igrec /usr/local/bin/igrec
COPY web ./web
ENV ADDR=:8080
ENV DATABASE_URL=sqlite://data/igrec.db
RUN mkdir -p /app/data
EXPOSE 8080
CMD ["igrec"]
