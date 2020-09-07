FROM golang:1.15-alpine AS build
WORKDIR /tmp
RUN apk add --update --no-cache ca-certificates make
RUN mkdir /app
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o todod main.go

FROM alpine:3.12
RUN apk add --update --no-cache ca-certificates tzdata
COPY --from=build /app/todod /bin/todod
USER guest
EXPOSE 8080
CMD todod
