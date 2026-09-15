# 第三方组件声明

dmdul 自身采用 MIT License。发布包包含以下组件的代码，其许可证独立适用；
完整许可证文本随发布包的 `licenses/` 目录提供，源码仓库对应 `docs/licenses/`。

| 组件 | 版本 | 许可证文本 |
| --- | --- | --- |
| Go runtime / standard library | 以 `go version -m dmdul` 输出为准 | `go-LICENSE.txt` |
| golang.org/x/text | v0.42.0 | `x-text-LICENSE.txt` |
| github.com/golang/snappy | v1.0.0 | `snappy-LICENSE.txt` |
| github.com/tjfoc/gmsm/sm3 | v1.4.1 | `gmsm-LICENSE.txt` |

Go 与 x/text：Copyright 2009 The Go Authors.

Snappy-Go：Copyright (c) 2011 The Snappy-Go Authors. All rights reserved.
遵循 BSD 3-Clause License，用于已验证的 HFS Snappy block 解压，未修改上游实现。

gmsm/sm3：Copyright Suzhou Tongji Fintech Research Institute 2017 All Rights Reserved.
该组件遵循 Apache License 2.0，dmdul 通过 Go module 引用，未修改上游 SM3 实现。
