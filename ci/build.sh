#!/bin/bash
function installLamp()
{
    apt-get install -y wget 
    wget https://s3-us-west-2.amazonaws.com/opsgeniedownloads/repo/opsgenie-lamp-2.5.0.zip
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

mkdir -p /root/.dockercache
eval $(aws ecr get-login --region us-west-2 --no-include-email)
for img in "golang:1.10.4" "alpine:3.8"
do
    if test -f "/root/.dockercache/$img.cache"
    then
        (gzip -c -d "/root/.dockercache/$img.cache" | docker load) &
    else 
        (docker pull $img ;docker save $img | gzip > "/root/.dockercache/$img.cache")
    fi
done
wait

docker build -t $ECR_REPO:b-$VERSION .
if [ "$REPLICON_GIT_BRANCH" = "master" ]
then
docker tag $ECR_REPO:b-$VERSION $ECR_REPO:m-$VERSION
docker push $ECR_REPO:m-$VERSION
else
#Push branch
echo "Branch Build"
docker push $ECR_REPO:b-$VERSION
fi
