VERSION 0.8
# DO NOT EDIT: Autogenerated headers
# KBT_VERSION 1.3.0 Generated by `kt dev earthly`
# This file will be regenerated automatically in each Jenkins run, and source of truth is `project.yml` file.
IMPORT github.com/kentik/earthly-lib:main AS kbt_lib
ARG --global BUILD_BASE_IMAGE=kt-build-generic-bullseye:master
ARG --global RUN_BASE_IMAGE=kt-run-generic-bullseye:master
ARG --global DEFAULT_DOCKER_REGISTRY=gcr.io/kentik-continuous-delivery
# DO NOT EDIT: Autogenerated headers

# You can put your personal stuff here

# DO NOT EDIT: Autogenerated code
kbt-build-all:
    FROM alpine
    BUILD +kbt-build
    BUILD +kbt-push
    BUILD +kbt-package-nomad-manifests
    ARG SKIP_TESTS=false
    IF [ "$SKIP_TESTS" = "false" ]
        BUILD +kbt-test
        BUILD +kbt-test-nomad-manifests
    END

# You can "overwrite" that step to install extra things in prepare phase, before all other installs or copying
KBT_PREPARE_IMAGE:
    FUNCTION

# You can "overwrite" that step to copy only required files, the less you copy the bigger chance to cache hit
KBT_COPY_SOURCE:
    FUNCTION
    COPY --keep-ts --dir . .

KBT_RUN_TESTS:
    FUNCTION
    DO --pass-args kbt_lib+GO_WITH_GITHUB --cmd="kbt_go_test_all"

KBT_RUN_BUILD:
    FUNCTION
    DO --pass-args kbt_lib+GO_WITH_GITHUB --cmd="unset GOPATH && make -f Makefile.kentik all install"

KBT_PREPARE_RUN_IMAGE:
    FUNCTION

kbt-prepare:
    FROM $DEFAULT_DOCKER_REGISTRY/$BUILD_BASE_IMAGE
    WORKDIR /build
    DO --pass-args kbt_lib+CONFIGURE_GITHUB
    DO --pass-args kbt_lib+KTB_GO_PREPARE
    DO --pass-args +KBT_PREPARE_IMAGE
    DO --pass-args +KBT_COPY_SOURCE
    DO --pass-args kbt_lib+KBT_GO_FLAGS --PROJECT_NAME=api-ratelimit

kbt-build:
    FROM +kbt-prepare
    # We need it here to pass that argument later on (probably its Earthly bug)
    ARG GO_TEST_FLAGS
    RUN mkdir -p /build/output
    DO --pass-args +KBT_RUN_BUILD

    SAVE ARTIFACT /build/output /

kbt-test:
    FROM +kbt-prepare

    ENV TEST_OUTPUT=/build/test-output
    IF [ -d "${TEST_OUTPUT}" ]
        RUN rm -rf ${TEST_OUTPUT}
    END
    RUN mkdir -p ${TEST_OUTPUT}/coverage/ ${TEST_OUTPUT}/unit
    COPY kbt_lib+test-scripts/scripts/* /bin/
    DO --pass-args +KBT_RUN_TESTS
    DO kbt_lib+GO_WITH_GITHUB --cmd="kbt_go_test_integration_coverage && kbt_go_lint"
    COPY --keep-ts +kbt-test-nomad-manifests/results_junit.xml ${TEST_OUTPUT}/unit/nomad/results_junit.xml
    WAIT
        RUN --no-cache echo Forcing save test artifacts
        SAVE ARTIFACT --keep-ts ${TEST_OUTPUT} AS LOCAL .
    END
    RUN kbt_check_test_results

kbt-image:
    FROM $DEFAULT_DOCKER_REGISTRY/$RUN_BASE_IMAGE
    
    COPY --pass-args +kbt-build/output /
    DO --pass-args +KBT_PREPARE_RUN_IMAGE

kbt-push:
    FROM +kbt-image
    DO --pass-args kbt_lib+KBT_PUSH --IMAGE_NAME=kentik-api-ratelimit

kbt-regenerate:
    FROM gcr.io/kentik-continuous-delivery/kt-ktools:master
    COPY --keep-ts project.yml Earthfile .
    RUN --entrypoint dev earthly
    SAVE ARTIFACT --keep-ts Earthfile AS LOCAL Earthfile

kbt-test-nomad-manifests:
    FROM github.com/kentik/kentik-nomad:main+integration-base-image
    COPY --keep-ts --dir project.yml nomad /data/service/ktools/repos/api-ratelimit/
    DO github.com/kentik/kentik-nomad:main+RUN_INTEGRATION_TESTS

kbt-package-nomad-manifests:
    FROM github.com/kentik/kentik-nomad:main+nomad-manifests-image
    COPY github.com/kentik/kentik-nomad:main+nomad-manifests-packs/manifests main-manifests
    COPY --keep-ts --dir nomad project.yml .

    ARG EARTHLY_TARGET_TAG_DOCKER
    ARG SANITIZED_BRANCH=$EARTHLY_TARGET_TAG_DOCKER
    ARG BUILD_NUMBER=0
    RUN ./package-nomad-manifests.sh apigw-ratelimit $SANITIZED_BRANCH.$BUILD_NUMBER service main
    RUN ./package-nomad-manifests.sh controlplane-ratelimit $SANITIZED_BRANCH.$BUILD_NUMBER service main
    SAVE ARTIFACT generated-manifests/*.zip AS LOCAL generated-manifests/
# DO NOT EDIT: Autogenerated code