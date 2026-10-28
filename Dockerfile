FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /rapid-entry .

FROM gcr.io/distroless/static-debian12
COPY --from=build /rapid-entry /rapid-entry
ENV ADDR=:8080 DB_PATH=/data/rapid.db
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/rapid-entry"]
