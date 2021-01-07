# Build crux stage
FROM golang:1.15.2-alpine3.12 as buildCrux

ENV GO111MODULE=on
WORKDIR /app
COPY . .
RUN go mod download && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/crux *.go


# Final stage
FROM alpine:3.12
RUN apk update && apk upgrade
COPY --from=buildCrux /app/bin/crux /bin
