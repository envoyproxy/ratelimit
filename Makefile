export GO111MODULE=on
PROJECT = ratelimit
REGISTRY ?= envoyproxy
IMAGE := $(REGISTRY)/$(PROJECT)
INTEGRATION_IMAGE := $(REGISTRY)/$(PROJECT)_integration
MODULE = github.com/envoyproxy/ratelimit
GIT_REF = $(shell git describe --tags --exact-match 2>/dev/null || git rev-parse --short=8 --verify HEAD)
VERSION ?= $(GIT_REF)
SHELL := /bin/bash

.PHONY: bootstrap
bootstrap: ;

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
	go fmt $(MODULE)/...

.PHONY: check_format
check_format: docs_format
	@gofmt -l $(shell go list -f '{{.Dir}}' ./...) | tee /dev/stderr | read && echo "Files failed gofmt" && exit 1 || true

.PHONY: compile
compile:
	mkdir -p ./bin
	go build -mod=readonly -o ./bin/ratelimit $(MODULE)/src/service_cmd
	go build -mod=readonly -o ./bin/ratelimit_client $(MODULE)/src/client_cmd
	go build -mod=readonly -o ./bin/ratelimit_config_check $(MODULE)/src/config_check_cmd

.PHONY: tests_unit
tests_unit: compile
	go test -race $(MODULE)/...

.PHONY: tests
tests: compile
	go test -race -tags=integration $(MODULE)/...

.PHONY: tests_with_redis
tests_with_redis: bootstrap_redis_tls tests_unit
	redis-server --port 6379 &
	redis-server --port 6380 &
	redis-server --port 6381 --requirepass password123 &
	redis-server --port 6382 --requirepass password123 &
	redis-server --port 6384 --requirepass password123 &
	redis-server --port 6385 --requirepass password123 &

	redis-server --port 6392 --requirepass password123 &
	redis-server --port 6393 --requirepass password123 --slaveof 127.0.0.1 6392 --masterauth password123 &
	mkdir 26394 && cp test/integration/conf/sentinel.conf 26394/sentinel.conf && redis-server 26394/sentinel.conf --sentinel --port 26394 &
	mkdir 26395 && cp test/integration/conf/sentinel.conf 26395/sentinel.conf && redis-server 26395/sentinel.conf --sentinel --port 26395 &
	mkdir 26396 && cp test/integration/conf/sentinel.conf 26396/sentinel.conf && redis-server 26396/sentinel.conf --sentinel --port 26396 &
	redis-server --port 6397 --requirepass password123 &
	redis-server --port 6398 --requirepass password123 --slaveof 127.0.0.1 6397 --masterauth password123 &
	mkdir 26399 && cp test/integration/conf/sentinel-pre-second.conf 26399/sentinel.conf && redis-server 26399/sentinel.conf --sentinel --port 26399 &
	mkdir 26400 && cp test/integration/conf/sentinel-pre-second.conf 26400/sentinel.conf && redis-server 26400/sentinel.conf --sentinel --port 26400 &
	mkdir 26401 && cp test/integration/conf/sentinel-pre-second.conf 26401/sentinel.conf && redis-server 26401/sentinel.conf --sentinel --port 26401 &

	mkdir 6386 && cd 6386 && redis-server --port 6386 --cluster-enabled yes --requirepass password123 &
	mkdir 6387 && cd 6387 && redis-server --port 6387 --cluster-enabled yes --requirepass password123 &
	mkdir 6388 && cd 6388 && redis-server --port 6388 --cluster-enabled yes --requirepass password123 &
	mkdir 6389 && cd 6389 && redis-server --port 6389 --cluster-enabled yes --requirepass password123 &
	mkdir 6390 && cd 6390 && redis-server --port 6390 --cluster-enabled yes --requirepass password123 &
	mkdir 6391 && cd 6391 && redis-server --port 6391 --cluster-enabled yes --requirepass password123 &
	memcached -u root --port 6394 -m 64 &
	sleep 2
	echo "yes" | redis-cli --cluster create -a password123 127.0.0.1:6386 127.0.0.1:6387 127.0.0.1:6388 --cluster-replicas 0
	echo "yes" | redis-cli --cluster create -a password123 127.0.0.1:6389 127.0.0.1:6390 127.0.0.1:6391 --cluster-replicas 0
	redis-cli --cluster check -a password123 127.0.0.1:6386
	redis-cli --cluster check -a password123 127.0.0.1:6389

	go test -race -tags=integration $(MODULE)/...

.PHONY: docker_tests
docker_tests:
	docker build -f Dockerfile.integration . -t $(INTEGRATION_IMAGE):$(VERSION) && \
	docker run $$(tty -s && echo "-it" || echo) $(INTEGRATION_IMAGE):$(VERSION)

.PHONY: docker_image
docker_image: docker_tests
	docker build . -t $(IMAGE):$(VERSION)

.PHONY: docker_push
docker_push: docker_image
	docker push $(IMAGE):$(VERSION)
