#!/bin/bash

echo "Running tests"

FILES=/test/scripts/*
for f in $FILES; do
	echo "Processing $f file..."
	# take action on each file. $f store current file name
	$f
	if [ $? -ne 0 ]; then
		echo "Failed file $f"
		exit 1
	fi
done
