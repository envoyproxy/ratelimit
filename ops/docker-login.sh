#!/usr/bin/env bash

set -ex

# login with presidio
aws --profile ${AWS_PROFILE:-deploy} --region ${AWS_REGION:-us-east-1} ecr get-login-password |
  docker login \
  --username AWS \
  --password-stdin \
  ${AWS_ACCOUNT_ID:-190066226418}.dkr.ecr.${AWS_REGION:-us-east-1}.amazonaws.com
