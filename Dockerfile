FROM alpine:3.19
RUN apk --no-cache add ca-certificates
COPY phasedb /usr/local/bin/phasedb
ENTRYPOINT ["phasedb"]
