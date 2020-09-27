# Build crux stage
FROM golang:1.15.2-alpine3.12 as buildCrux

ARG GITHUB_ACCESS_KEY
ENV GO111MODULE=on
WORKDIR /app
RUN mkdir /user && \
    echo 'nobody:x:65534:65534:nobody:/:' > /user/passwd && \
    echo 'nobody:x:65534:' > /user/group
RUN apk update && \
    apk add --no-cache ca-certificates git
RUN git config --global url."https://${GITHUB_ACCESS_KEY}:@github.com/".insteadOf "https://github.com/"
RUN git clone https://github.com/bsostech/crux.git
RUN cd crux && git checkout fubon/add-trusted-CA && go mod download
RUN cd /app/crux && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/crux *.go


# Final stage
FROM alpine:3.12
COPY --from=buildCrux /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=buildCrux /app/crux/bin/crux /bin
USER nobody:nobody