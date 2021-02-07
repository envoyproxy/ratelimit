#!/usr/bin/env bash

trap "exit" INT TERM ERR
trap "kill 0" EXIT

redis-server --port 6381 --requirepass password123 &
redis-server --port 6382 --requirepass password123 &

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
sleep 2
echo "yes" | redis-cli --cluster create -a password123 127.0.0.1:6386 127.0.0.1:6387 127.0.0.1:6388 --cluster-replicas 0
echo "yes" | redis-cli --cluster create -a password123 127.0.0.1:6389 127.0.0.1:6390 127.0.0.1:6391 --cluster-replicas 0
redis-cli --cluster check -a password123 127.0.0.1:6386
redis-cli --cluster check -a password123 127.0.0.1:6389

wait