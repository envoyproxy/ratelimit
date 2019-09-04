ifeq ("$(GOPATH)","")
$(error GOPATH must be set)
endif

SHELL := /bin/bash
GOREPO := ${GOPATH}/src/github.com/lyft/ratelimit

.PHONY: bootstrap
bootstrap:
	script/install-glide
	glide install

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
	go fmt $(shell glide nv)

.PHONY: check_format
check_format: docs_format
	@gofmt -l $(shell glide nv | sed 's/\.\.\.//g') | tee /dev/stderr | read && echo "Files failed gofmt" && exit 1 || true

.PHONY: compile
compile:
	mkdir -p ${GOREPO}/bin
	cd ${GOREPO}/src/service_cmd && go build -o ratelimit ./ && mv ./ratelimit ${GOREPO}/bin
	cd ${GOREPO}/src/client_cmd && go build -o ratelimit_client ./ && mv ./ratelimit_client ${GOREPO}/bin
	cd ${GOREPO}/src/config_check_cmd && go build -o ratelimit_config_check ./ && mv ./ratelimit_config_check ${GOREPO}/bin

.PHONY: tests_unit
tests_unit: compile
	go test -race ./...

.PHONY: tests
tests: compile
	go test -race -tags=integration ./...

.PHONY: docker
docker: tests
	docker build . -t lyft/ratelimit:`git rev-parse HEAD`
