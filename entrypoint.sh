#!/bin/sh

export REDIS_AUTH=$(/usr/local/bin/aws ssm get-parameter --name ${REDIS_AUTH_SSM_PATH} --with-decryption --query 'Parameter.Value' --output text)

nohup /sync_config.sh &
/bin/ratelimit
