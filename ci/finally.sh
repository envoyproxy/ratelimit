#!/bin/bash
function configureAlerts() {
  if [ "$REPLICON_GIT_BRANCH" = "master" ]; then
    if [ $CODEBUILD_BUILD_SUCCEEDING -eq 0 ]; then
      lamp createAlert --apiKey $OPS_GENIE_API_KEY --alias $PROJECTNAME-master-build --message "$PROJECTNAME master $VERSION failing" --source "$PROJECTNAME CodeBuild CD us-west-2"
    else
      lamp closeAlert --apiKey $OPS_GENIE_API_KEY --alias $PROJECTNAME-master-build
    fi
  fi
}

. ./ci/set_env.sh
configureAlerts