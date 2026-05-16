#!/bin/sh
exec "$TEST_BINARY" -test.run '^TestE2EHelperProcess$' -- "$@"
