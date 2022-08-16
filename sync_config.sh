#!/bin/sh

cd /srv/runtime_data/current

while true
do
  /usr/local/bin/aws s3 sync --delete ${RLS_S3_PATH} data/

  [ $? -eq 0 ] && /usr/local/bin/python3 data/${RLS_SCRIPT_DIR}config-generator.py \
    --static data/${RLS_STATIC_DIR} \
    --blacklist data/${RLS_BLACKLIST_DIR} \
    --override data/${RLS_OVERRIDE_DIR} \
    --output /srv/runtime_data/current/validate_config/new_config.yaml \
    --swimlane ${SWIMLANE} > /dev/null

  [ $? -eq 0 ] && mv validate_config/new_config.yaml config/config.yaml

  sleep 300
done
