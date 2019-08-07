#!/bin/bash
if [ -f version ]; then
    export VERSION=$(cat version)
else
    TIMESTAMP=$(date +%s)
    export VERSION="1.0.${TIMESTAMP//[$'\t\r\n']}"
    echo -n "$VERSION" > version
fi
export PROJECTNAME=ratelimit
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

echo "==> AWS CodeBuild Extra Environment Variables:"
echo "==> REPLICON_GIT_AUTHOR = $REPLICON_GIT_AUTHOR"
echo "==> REPLICON_GIT_AUTHOR_EMAIL = $REPLICON_GIT_AUTHOR_EMAIL"
echo "==> REPLICON_GIT_BRANCH = $REPLICON_GIT_BRANCH"
echo "==> REPLICON_GIT_CLEAN_BRANCH = $REPLICON_GIT_CLEAN_BRANCH"
echo "==> REPLICON_GIT_ESCAPED_BRANCH = $REPLICON_GIT_ESCAPED_BRANCH"
echo "==> REPLICON_GIT_MESSAGE = $REPLICON_GIT_MESSAGE"
echo "==> VERSION = $VERSION"