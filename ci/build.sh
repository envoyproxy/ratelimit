#!/bin/bash
function installLamp()
{
    apt-get install -y wget 
    wget https://s3-us-west-2.amazonaws.com/opsgeniedownloads/repo/opsgenie-lamp-2.5.0.zip
    unzip opsgenie-lamp-2.5.0.zip -d opsgenie
    mv opsgenie/lamp/lamp /usr/local/bin
    rm -rf opsgenie*
}
set -eo nounset
export REPLICON_GIT_BRANCH="$(git symbolic-ref HEAD --short 2>/dev/null)"
if [ "$REPLICON_GIT_BRANCH" = "" ] ; then
  REPLICON_GIT_BRANCH="$(git branch -a --contains HEAD | sed -n 2p | awk '{ printf $1 }')";
  export REPLICON_GIT_BRANCH=${REPLICON_GIT_BRANCH#remotes/origin/};
fi
export REPLICON_GIT_CLEAN_BRANCH="$(echo $REPLICON_GIT_BRANCH | tr '/' '.')"
export REPLICON_GIT_ESCAPED_BRANCH="$(echo $REPLICON_GIT_CLEAN_BRANCH | sed -e 's/[]\/$*.^[]/\\\\&/g')"
export REPLICON_GIT_MESSAGE="$(git log -1 --pretty=%B)"
export REPLICON_GIT_AUTHOR="$(git log -1 --pretty=%an)"
export REPLICON_GIT_AUTHOR_EMAIL="$(git log -1 --pretty=%ae)"
export REPLICON_GIT_SHA="$(git rev-parse HEAD)"

echo "==> AWS CodeBuild Extra Environment Variables:"
echo "==> REPLICON_GIT_AUTHOR = $REPLICON_GIT_AUTHOR"
echo "==> REPLICON_GIT_AUTHOR_EMAIL = $REPLICON_GIT_AUTHOR_EMAIL"
echo "==> REPLICON_GIT_BRANCH = $REPLICON_GIT_BRANCH"
echo "==> REPLICON_GIT_CLEAN_BRANCH = $REPLICON_GIT_CLEAN_BRANCH"
echo "==> REPLICON_GIT_ESCAPED_BRANCH = $REPLICON_GIT_ESCAPED_BRANCH"
echo "==> REPLICON_GIT_MESSAGE = $REPLICON_GIT_MESSAGE"
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

docker build -t $ECR_REPO:b-$REPLICON_GIT_SHA .
if [ "$REPLICON_GIT_BRANCH" = "master" ]
then
docker tag $ECR_REPO:b-$REPLICON_GIT_SHA $ECR_REPO:m-$REPLICON_GIT_SHA
docker push $ECR_REPO:m-$REPLICON_GIT_SHA
else
#Push branch
echo "Branch Build"
docker push $ECR_REPO:b-$REPLICON_GIT_SHA
fi
