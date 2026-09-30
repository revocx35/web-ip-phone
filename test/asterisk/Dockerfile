# Disposable Asterisk PBX for integration tests (never exposed; host networking on the dev box).
FROM alpine:3.22
RUN apk add --no-cache asterisk asterisk-sounds-en asterisk-sounds-moh \
 && rm -f /etc/asterisk/*.conf
COPY conf/ /etc/asterisk/
CMD ["asterisk", "-f", "-U", "asterisk", "-G", "asterisk", "-vvv"]
