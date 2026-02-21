# ========================================================
# Stage: Builder
# ========================================================
FROM golang:1.25-alpine AS builder
WORKDIR /app
ARG TARGETARCH

RUN apk --no-cache --update add \
  build-base \
  gcc \
  curl \
  unzip \
  bash

COPY . .

# Make all .sh files executable and fix line endings if needed
RUN chmod +x *.sh && \
    sed -i 's/\r$//' *.sh && \
    ls -la DockerInit.sh

ENV CGO_ENABLED=1
ENV CGO_CFLAGS="-D_LARGEFILE64_SOURCE"
RUN go build -ldflags "-w -s" -o build/x-ui main.go
RUN bash DockerInit.sh "$TARGETARCH"

# ========================================================
# Stage: Final Image of SharX
# ========================================================
FROM alpine
ENV TZ=Asia/Tehran
WORKDIR /app

RUN apk add --no-cache --update \
  ca-certificates \
  tzdata \
  fail2ban \
  bash \
  postgresql-client

COPY --from=builder /app/build/ /app/
COPY --from=builder /app/DockerEntrypoint.sh /app/

# Configure fail2ban
RUN rm -f /etc/fail2ban/jail.d/alpine-ssh.conf \
  && cp /etc/fail2ban/jail.conf /etc/fail2ban/jail.local \
  && sed -i "s/^\[ssh\]$/&\nenabled = false/" /etc/fail2ban/jail.local \
  && sed -i "s/^\[sshd\]$/&\nenabled = false/" /etc/fail2ban/jail.local \
  && sed -i "s/#allowipv6 = auto/allowipv6 = auto/g" /etc/fail2ban/fail2ban.conf

RUN chmod +x \
  /app/DockerEntrypoint.sh \
  /app/x-ui

ENV XUI_ENABLE_FAIL2BAN="true"
EXPOSE 2053
VOLUME [ "/etc/x-ui" ]
CMD [ "./x-ui" ]
ENTRYPOINT [ "/app/DockerEntrypoint.sh" ]
