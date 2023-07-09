# starcharts

[![Build Status](https://img.shields.io/github/actions/workflow/status/caarlos0/starcharts/build.yml?style=for-the-badge)](https://github.com/caarlos0/starcharts/actions?workflow=build)
[![Coverage Status](https://img.shields.io/codecov/c/gh/caarlos0/starcharts.svg?logo=codecov&style=for-the-badge)](https://codecov.io/gh/caarlos0/starcharts)
[![](http://img.shields.io/badge/godoc-reference-5272B4.svg?style=for-the-badge)](http://godoc.org/github.com/caarlos0/starcharts)

Plot your repo stars over time!

## Usage

```console
go run main.go
```

Then browse http://localhost:3000/me/myrepo .

V1.0 计划做的功能：

- 支持2-3个代码仓库的star时间对比（目前只支持2-3个）
- 重构getStargazersPage()函数，重新设计缓存

