# InfluxDB data extractor

This extractor tool intends to help you move data from influxdb to another data bucket downloading CSV**¹** files.

**¹** CSV 'cause that file type are smaller than JSON and download more faster.

## Setup

`make setup`

## Run locally

`make run`

## Compile (on docker container)

`make docker`

## Format code

`make format`

## Lint

`make lint`

## Clean downloaded files (be aware)

`make sanitize`
