# mmdb-import 
This tool is used to import a JSON file into a ([MaxMind DB file format](https://github.com/maxmind/MaxMind-DB)). This tool can import JSON, validate JSON and output existing mmdb file and their metadata.

## Installation
Easy way to install this tool is to use `go install` command.
```bash
go install github.com/7c/mmdbimport@latest
```

## Build
If you want to build this tool from source, you can use `make` command.
```bash
$ make build
$ bin/mmdbimport -h
```

## Usage
```
usage: mmdbimport [<flags>]

A tool to import JSON into MMDB files

Flags:
  -h, --[no-]help             Show context-sensitive help (also try --help-long and --help-man).
  -c, --check=CHECK           Check JSON file for errors without building MMDB
  -i, --input=INPUT           Input JSON file path
  -v, --verify=VERIFY         Verify and display MMDB file information
  -V, --verify-verbose=VERIFY-VERBOSE  
                              Verify and display MMDB file information
  -o, --output="output.mmdb"  Output MMDB file path
  -r, --record-size=28        Record size (24, 28, or 32)
```

## import json
this tool need a valid json file with proper format, it does validate the format of the json file. Check `etc/` folder for examples.
```bash
$ mmdbimport -i etc/input.ok.json -o output.mmdb
```


## viewing existing mmdb files
if you use '-V' flag, it will show all the records in the mmdb file and their metadata.
```bash
$ mmdbimport -v etc/GeoIP2-City-Test.mmdb
MMDB file: etc/GeoIP2-City-Test.mmdb

Database Information:
  Binary Format: 2.0
  IP Version: 6
  Record Size: 28 bits
  Node Count: 1542

Metadata:
  Database Type: GeoIP2-City
  Description:
    en: GeoIP2 City Test Database (fake GeoIP2 data, for example purposes only)
    zh: 小型数据库
  Languages: en, zh
  Build Timestamp: 2024-11-21T13:33:48-05:00

Statistics:
  Total Networks: 248
```

## other mmdbtools
[mmdbinspect](https://github.com/maxmind/mmdbinspect) tool to validate mmdb files might be useful made by MaxMind.
