# Disposable PostgreSQL whose system clock a test controls (C6 cross-day refund gate).
# libfaketime re-reads /clock/faketime on every time call ("+0" = real time, "-3600" = one hour
# behind, …). Test use only: never build production images from this file.
ARG PG_MAJOR=16
FROM postgres:${PG_MAJOR}-alpine
RUN apk add --no-cache libfaketime \
 && ln -s "$(find /usr/lib -name 'libfaketime.so.1' | head -n1)" /usr/local/lib/libfaketime.so.1 \
 && test -e /usr/local/lib/libfaketime.so.1
ENV LD_PRELOAD=/usr/local/lib/libfaketime.so.1 \
    FAKETIME_TIMESTAMP_FILE=/clock/faketime \
    FAKETIME_NO_CACHE=1 \
    FAKETIME_DONT_FAKE_MONOTONIC=1
