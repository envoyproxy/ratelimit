#!/bin/bash

COMPONENT_NAME="ratelimit"
APP_REPO_REGIONS="ap-southeast-2 ap-southeast-4 ca-central-1 eu-central-1 eu-west-1 eu-west-3 us-east-1 us-east-2 us-west-2"
DOCKER_REPO="$COMPONENT_NAME/app"

function installLamp()
{
    apt-get install -y wget
    wget -q https://s3-us-west-2.amazonaws.com/opsgeniedownloads/repo/opsgenie-lamp-2.5.0.zip
    unzip opsgenie-lamp-2.5.0.zip -d opsgenie
    mv opsgenie/lamp/lamp /usr/local/bin
    rm -rf opsgenie*
}
function setEnvs()
{
. ci/set_env.sh
}
set -eo nounset

setEnvs

installLamp &
docker login -u "$DOCKER_USER" -p "$DOCKER_PASSWORD"
for REGION in $APP_REPO_REGIONS; do
  eval $(aws ecr get-login --region "$REGION" --no-include-email)
done

set -e
apt-get update -y
apt-get install -y  redis-server

make tests_with_redis

docker version
curl -JLO https://github.com/docker/buildx/releases/download/v0.4.2/buildx-v0.4.2.linux-amd64
mkdir -p ~/.docker/cli-plugins
mv buildx-v0.4.2.linux-amd64 ~/.docker/cli-plugins/docker-buildx
chmod a+rx ~/.docker/cli-plugins/docker-buildx
docker run --privileged --rm tonistiigi/binfmt --install all
docker buildx create --name multi-arch-builder --driver-opt network=host --use

TAG="b-$VERSION"
if [ "$REPLICON_GIT_BRANCH" = "master" ]; then
  TAG="m-$VERSION"
fi

CORE_TAGS=""
AMD64_TAGS=""
ARM64_TAGS=""
for REGION in $APP_REPO_REGIONS; do
    REGION_TAG="434423891815.dkr.ecr.$REGION.amazonaws.com/$DOCKER_REPO:$TAG"
    CORE_TAGS="$CORE_TAGS --tag $REGION_TAG"
    AMD64_TAGS="$AMD64_TAGS --tag $REGION_TAG-amd64"
    ARM64_TAGS="$ARM64_TAGS --tag $REGION_TAG-arm64"
done

docker buildx build --platform linux/amd64,linux/arm64 $CORE_TAGS --push .
docker buildx build --platform linux/amd64 $AMD64_TAGS --push .
docker buildx build --platform linux/arm64 $ARM64_TAGS --push .

docker buildx rm multi-arch-builder
