#!/bin/sh
# Resources used by the SQS and Kinesis acceptance tests.
set -e
awslocal sqs create-queue --queue-name tf-acc
awslocal kinesis create-stream --stream-name tf-acc --shard-count 1
awslocal kinesis wait stream-exists --stream-name tf-acc
touch /tmp/tf-acc-ready
