## mackerel-plugin-redshift-import-stats

### Installation

Use [mkr](https://github.com/mackerelio/mkr).

```
# mkr plugin install kayac/mackerel-plugin-redshift-import-stats
```

Recommended to specify the version.


### Usage

```
Usage:
  mackerel-plugin-redshift-import-stats [OPTIONS]

Application Options:
  -H, --host=hostname                                       Redshift endpoint
  -d, --database=database-name                              Database name
  -p, --port=5439                                           Port number (default: 5439)
  -u, --user=root                                           user name
  -P, --password=password                                   password
      --prefix=
      --tempfile=
  -t, --target=table_name:column_name:column_type:offset    Describe the target table, time column, and time column type with colon separated value.

Help Options:
  -h, --help                                                Show this help message
```

### Example of mackerel-agent.conf

```
[plugin.metrics.redshift-import-stats]
command = '''
/opt/mackerel-agent/plugins/bin/mackerel-plugin-redshift-import-stats \
    -H example.endpoint.ap-northeast-1.redshift.amazonaws.com \
    -d mydatabase \
    -u user_ro \
    -P 3x4mp1ep455w0rd \
    -t receipt:created_at:timestamp \
    -t user_action_log:time:integer \
    -t campaign:time:integer:128
'''
```

## Author

KAYAC Inc.

## License

Copyright 2014 Hatena Co., Ltd.

Copyright 2018 KAYAC Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

