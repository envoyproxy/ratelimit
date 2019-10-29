SHELL := /bin/bash

export REPOSITORY=ratelimit
include boilerplate/lyft/docker_build/Makefile

.PHONY: update_boilerplate
update_boilerplate:
	@boilerplate/update.sh

.PHONY: bootstrap_tests
bootstrap_tests:
	cd ./vendor/github.com/golang/mock/mockgen && go install
define REDIS_STUNNEL
cert = private.pem
pid = /var/run/stunnel.pid
[redis]
accept = 127.0.0.1:16381
connect = 127.0.0.1:6381
endef
define REDIS_PER_SECOND_STUNNEL
cert = private.pem
pid = /var/run/stunnel-2.pid
[redis]
accept = 127.0.0.1:16382
connect = 127.0.0.1:6382
endef
export REDIS_STUNNEL
export REDIS_PER_SECOND_STUNNEL
redis.conf:
	echo "$$REDIS_STUNNEL" >> $@
redis-per-second.conf:
	echo "$$REDIS_PER_SECOND_STUNNEL" >> $@
.PHONY: bootstrap_redis_tls
bootstrap_redis_tls: redis.conf redis-per-second.conf
	openssl req -new -newkey rsa:4096 -days 365 -nodes -x509 \
    -subj "/C=US/ST=Denial/L=Springfield/O=Dis/CN=localhost" \
    -keyout key.pem  -out cert.pem
	cat key.pem cert.pem > private.pem
	sudo cp cert.pem /usr/local/share/ca-certificates/redis-stunnel.crt
	chmod 640 key.pem cert.pem private.pem
	sudo update-ca-certificates
	sudo stunnel redis.conf
	sudo stunnel redis-per-second.conf
.PHONY: docs_format
docs_format:
	script/docs_check_format

.PHONY: fix_format
fix_format:
	script/docs_fix_format
	go fmt ./...

.PHONY: check_format
check_format: docs_format
	@gofmt -l $(shell go list -f '{{.Dir}}' ./...) | tee /dev/stderr | read && echo "Files failed gofmt" && exit 1 || true

.PHONY: compile
compile:
	mkdir -p bin
	go build -o bin/ratelimit ./src/service_cmd
	go build -o bin/ratelimit_client ./src/client_cmd
	go build -o bin/ratelimit_config_check ./src/config_check_cmd

.PHONY: tests_unit
tests_unit: compile
	go test -race ./...

.PHONY: tests
tests: compile
	go test -race -tags=integration ./...
