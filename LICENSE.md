MIT License

Copyright (c) 2023 apimgr

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.


---

## Third-Party Licenses

This software includes the following dependencies shipped by the server or CLI. Each attribution links to its upstream license text. The package list is generated from `go-licenses csv ./src/...`; reviewed overrides are documented in `.github/license-overrides.txt`.

### Embedded GeoNames datasets

Citylist includes global city records and United States postal-code records from GeoNames. GeoNames data is available under the Creative Commons Attribution 4.0 International License (CC BY 4.0). Attribution: GeoNames, [geonames.org](https://www.geonames.org/). The records are embedded and indexed for read-only lookups; United States postal records without a two-letter state code, including military routing entries, are excluded. Source details and snapshot checksums are in [DATA-SOURCES.md](DATA-SOURCES.md). See the [GeoNames data export](https://www.geonames.org/export/) and [CC BY 4.0 legal code](https://creativecommons.org/licenses/by/4.0/legalcode).

| Library | Version | License | Copyright / terms |
|---------|---------|---------|-------------------|
| [charm.land/bubbles/v2](https://github.com/charmbracelet/bubbles/blob/v2.2.2/LICENSE) | v2.2.2 | MIT | See linked license |
| [charm.land/bubbletea/v2](https://github.com/charmbracelet/bubbletea/blob/v2.1.2/LICENSE) | v2.1.2 | MIT | See linked license |
| [charm.land/lipgloss/v2](https://github.com/charmbracelet/lipgloss/blob/v2.1.1/LICENSE) | v2.1.1 | MIT | See linked license |
| [cloud.google.com/go/auth](https://github.com/googleapis/google-cloud-go/blob/dbc26066f70a/auth/LICENSE) | v0.24.1-0.20261001053825-dbc26066f70a | Apache-2.0 | See linked license |
| [cloud.google.com/go/auth/oauth2adapt](https://github.com/googleapis/google-cloud-go/blob/auth/oauth2adapt/v0.3.1/auth/oauth2adapt/LICENSE) | v0.3.1 | Apache-2.0 | See linked license |
| [cloud.google.com/go/compute/metadata](https://github.com/googleapis/google-cloud-go/blob/compute/metadata/v0.10.0/compute/metadata/LICENSE) | v0.10.0 | Apache-2.0 | See linked license |
| [filippo.io/edwards25519](https://github.com/FiloSottile/edwards25519/blob/v1.1.0/LICENSE) | v1.1.0 | BSD-3-Clause | See linked license |
| [github.com/AdamSLevy/jsonrpc2/v14](https://github.com/AdamSLevy/jsonrpc2/blob/v14.1.0/LICENSE) | v14.1.0 | MIT | See linked license |
| [github.com/akamai/AkamaiOPEN-edgegrid-golang/v14/pkg](https://github.com/akamai/AkamaiOPEN-edgegrid-golang/blob/v14.1.0/LICENSE) | v14.1.0 | Apache-2.0 | See linked license |
| [github.com/alibabacloud-go/alibabacloud-gateway-pop/client](https://github.com/alibabacloud-go/alibabacloud-gateway-pop/blob/master/LICENSE) | v0.0.8 | Apache-2.0 | See linked license |
| [github.com/alibabacloud-go/alibabacloud-gateway-spi/client](https://github.com/alibabacloud-go/alibabacloud-gateway-spi/blob/v0.0.5/LICENSE) | v0.0.5 | Apache-2.0 | See linked license |
| [github.com/alibabacloud-go/darabonba-array/client](https://github.com/alibabacloud-go/darabonba-array/blob/master/LICENSE) | v0.1.0 | Apache-2.0 | See linked license |
| [github.com/alibabacloud-go/darabonba-encode-util/client](https://github.com/alibabacloud-go/darabonba-encode-util/blob/master/LICENSE) | v0.0.2 | Apache-2.0 | See linked license |
| [github.com/alibabacloud-go/darabonba-map/client](https://github.com/alibabacloud-go/darabonba-map/blob/master/LICENSE) | v0.0.2 | Apache-2.0 | See linked license |
| [github.com/alibabacloud-go/darabonba-openapi/v2](https://github.com/alibabacloud-go/darabonba-openapi/blob/v2.2.5/LICENSE) | v2.2.5 | Apache-2.0 | See linked license |
| [github.com/alibabacloud-go/darabonba-signature-util/client](https://github.com/alibabacloud-go/darabonba-signature-util/blob/master/LICENSE) | v0.0.7 | Apache-2.0 | See linked license |
| [github.com/alibabacloud-go/darabonba-string/client](https://github.com/alibabacloud-go/darabonba-string/blob/master/LICENSE) | v1.0.2 | Apache-2.0 | See linked license |
| [github.com/alibabacloud-go/debug/debug](https://github.com/alibabacloud-go/debug/blob/v1.0.1/LICENSE) | v1.0.1 | Apache-2.0 | See linked license |
| [github.com/alibabacloud-go/endpoint-util/service](https://github.com/alibabacloud-go/endpoint-util/blob/master/LICENSE) | v1.1.0 | Apache-2.0 | See linked license |
| [github.com/alibabacloud-go/openapi-util/service](https://github.com/alibabacloud-go/openapi-util/blob/v0.1.2/LICENSE) | v0.1.2 | Apache-2.0 | See linked license |
| [github.com/alibabacloud-go/tea](https://github.com/alibabacloud-go/tea/blob/v1.5.4/LICENSE) | v1.5.4 | Apache-2.0 | See linked license |
| [github.com/alibabacloud-go/tea-utils/v2/service](https://github.com/alibabacloud-go/tea-utils/blob/v2.0.9/LICENSE) | v2.0.9 | Apache-2.0 | See linked license |
| [github.com/aliyun/credentials-go/credentials](https://github.com/aliyun/credentials-go/blob/v1.4.7/LICENSE) | v1.4.7 | Apache-2.0 | See linked license |
| [github.com/antlr4-go/antlr/v4](https://github.com/antlr4-go/antlr/blob/v4.13.0/LICENSE) | v4.13.0 | BSD-3-Clause | See linked license |
| [github.com/atotto/clipboard](https://github.com/atotto/clipboard/blob/v0.1.4/LICENSE) | v0.1.4 | BSD-3-Clause | See linked license |
| [github.com/aws/aws-sdk-go-v2](https://github.com/aws/aws-sdk-go-v2/blob/v1.48.0/LICENSE.txt) | v1.48.0 | Apache-2.0 | See linked license |
| [github.com/aws/aws-sdk-go-v2/config](https://github.com/aws/aws-sdk-go-v2/blob/config/v1.33.19/config/LICENSE.txt) | v1.33.19 | Apache-2.0 | See linked license |
| [github.com/aws/aws-sdk-go-v2/credentials](https://github.com/aws/aws-sdk-go-v2/blob/credentials/v1.21.1/credentials/LICENSE.txt) | v1.21.1 | Apache-2.0 | See linked license |
| [github.com/aws/aws-sdk-go-v2/feature/ec2/imds](https://github.com/aws/aws-sdk-go-v2/blob/feature/ec2/imds/v1.20.9/feature/ec2/imds/LICENSE.txt) | v1.20.9 | Apache-2.0 | See linked license |
| [github.com/aws/aws-sdk-go-v2/internal/configsources](https://github.com/aws/aws-sdk-go-v2/blob/internal/configsources/v1.5.12/internal/configsources/LICENSE.txt) | v1.5.12 | Apache-2.0 | See linked license |
| [github.com/aws/aws-sdk-go-v2/internal/endpoints/v2](https://github.com/aws/aws-sdk-go-v2/blob/internal/endpoints/v2.8.12/internal/endpoints/v2/LICENSE.txt) | v2.8.12 | Apache-2.0 | See linked license |
| [github.com/aws/aws-sdk-go-v2/internal/sync/singleflight](https://github.com/aws/aws-sdk-go-v2/blob/v1.48.0/internal/sync/singleflight/LICENSE) | v1.48.0 | BSD-3-Clause | See linked license |
| [github.com/aws/aws-sdk-go-v2/internal/v4a](https://github.com/aws/aws-sdk-go-v2/blob/internal/v4a/v1.5.12/internal/v4a/LICENSE.txt) | v1.5.12 | Apache-2.0 | See linked license |
| [github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding](https://github.com/aws/aws-sdk-go-v2/blob/service/internal/accept-encoding/v1.13.25/service/internal/accept-encoding/LICENSE.txt) | v1.13.25 | Apache-2.0 | See linked license |
| [github.com/aws/aws-sdk-go-v2/service/internal/presigned-url](https://github.com/aws/aws-sdk-go-v2/blob/service/internal/presigned-url/v1.14.12/service/internal/presigned-url/LICENSE.txt) | v1.14.12 | Apache-2.0 | See linked license |
| [github.com/aws/aws-sdk-go-v2/service/lightsail](https://github.com/aws/aws-sdk-go-v2/blob/service/lightsail/v1.72.0/service/lightsail/LICENSE.txt) | v1.72.0 | Apache-2.0 | See linked license |
| [github.com/aws/aws-sdk-go-v2/service/route53](https://github.com/aws/aws-sdk-go-v2/blob/service/route53/v1.71.0/service/route53/LICENSE.txt) | v1.71.0 | Apache-2.0 | See linked license |
| [github.com/aws/aws-sdk-go-v2/service/signin](https://github.com/aws/aws-sdk-go-v2/blob/service/signin/v1.13.1/service/signin/LICENSE.txt) | v1.13.1 | Apache-2.0 | See linked license |
| [github.com/aws/aws-sdk-go-v2/service/sso](https://github.com/aws/aws-sdk-go-v2/blob/service/sso/v1.39.5/service/sso/LICENSE.txt) | v1.39.5 | Apache-2.0 | See linked license |
| [github.com/aws/aws-sdk-go-v2/service/ssooidc](https://github.com/aws/aws-sdk-go-v2/blob/service/ssooidc/v1.44.5/service/ssooidc/LICENSE.txt) | v1.44.5 | Apache-2.0 | See linked license |
| [github.com/aws/aws-sdk-go-v2/service/sts](https://github.com/aws/aws-sdk-go-v2/blob/service/sts/v1.52.0/service/sts/LICENSE.txt) | v1.52.0 | Apache-2.0 | See linked license |
| [github.com/aws/smithy-go](https://github.com/aws/smithy-go/blob/v1.28.9/LICENSE) | v1.28.9 | Apache-2.0 | See linked license |
| [github.com/aws/smithy-go/internal/sync/singleflight](https://github.com/aws/smithy-go/blob/v1.28.9/internal/sync/singleflight/LICENSE) | v1.28.9 | BSD-3-Clause | See linked license |
| [github.com/aws/smithy-go/transport/http/protocol/internal/json/internal/stdlib](https://github.com/aws/smithy-go/blob/v1.28.9/transport/http/protocol/internal/json/internal/stdlib/LICENSE) | v1.28.9 | BSD-3-Clause | See linked license |
| [github.com/aymerick/douceur](https://github.com/aymerick/douceur/blob/v0.2.0/LICENSE) | v0.2.0 | MIT | See linked license |
| [github.com/aziontech/azionapi-go-sdk/idns](https://github.com/aziontech/azionapi-go-sdk/blob/v0.148.0/LICENSE) | v0.148.0 | MIT | See linked license |
| [github.com/Azure/azure-sdk-for-go/sdk/azcore](https://github.com/Azure/azure-sdk-for-go/blob/sdk/azcore/v1.24.0/sdk/azcore/LICENSE.txt) | v1.24.0 | MIT | See linked license |
| [github.com/Azure/azure-sdk-for-go/sdk/azidentity](https://github.com/Azure/azure-sdk-for-go/blob/sdk/azidentity/v1.15.0/sdk/azidentity/LICENSE.txt) | v1.15.0 | MIT | See linked license |
| [github.com/Azure/azure-sdk-for-go/sdk/internal](https://github.com/Azure/azure-sdk-for-go/blob/sdk/internal/v1.13.0/sdk/internal/LICENSE.txt) | v1.13.0 | MIT | See linked license |
| [github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns](https://github.com/Azure/azure-sdk-for-go/blob/sdk/resourcemanager/dns/armdns/v1.2.0/sdk/resourcemanager/dns/armdns/LICENSE.txt) | v1.2.0 | MIT | See linked license |
| [github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/privatedns/armprivatedns/v2](https://github.com/Azure/azure-sdk-for-go/blob/sdk/resourcemanager/privatedns/armprivatedns/v2.0.0/sdk/resourcemanager/privatedns/armprivatedns/LICENSE.txt) | v2.0.0 | MIT | See linked license |
| [github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph](https://github.com/Azure/azure-sdk-for-go/blob/sdk/resourcemanager/resourcegraph/armresourcegraph/v0.10.0/sdk/resourcemanager/resourcegraph/armresourcegraph/LICENSE.txt) | v0.10.0 | MIT | See linked license |
| [github.com/AzureAD/microsoft-authentication-library-for-go/apps](https://github.com/AzureAD/microsoft-authentication-library-for-go/blob/v1.10.1/LICENSE) | v1.10.1 | MIT | See linked license |
| [github.com/baidubce/bce-sdk-go](https://github.com/baidubce/bce-sdk-go/blob/v0.9.279/LICENSE) | v0.9.279 | Apache-2.0 | See linked license |
| [github.com/benbjohnson/clock](https://github.com/benbjohnson/clock/blob/v1.3.5/LICENSE) | v1.3.5 | MIT | See linked license |
| [github.com/beorn7/perks/quantile](https://github.com/beorn7/perks/blob/v1.0.1/LICENSE) | v1.0.1 | MIT | See linked license |
| [github.com/bodgit/gssapi](https://github.com/bodgit/gssapi/blob/v0.0.4/LICENSE) | v0.0.4 | BSD-3-Clause | See linked license |
| [github.com/bodgit/tsig](https://github.com/bodgit/tsig/blob/v1.3.1/LICENSE) | v1.3.1 | BSD-3-Clause | See linked license |
| [github.com/boombuler/barcode](https://github.com/boombuler/barcode/blob/v1.0.1/LICENSE) | v1.0.1 | MIT | See linked license |
| [github.com/cenkalti/backoff/v4](https://github.com/cenkalti/backoff/blob/v4.3.0/LICENSE) | v4.3.0 | MIT | See linked license |
| [github.com/cenkalti/backoff/v5](https://github.com/cenkalti/backoff/blob/v5.0.3/LICENSE) | v5.0.3 | MIT | See linked license |
| [github.com/cespare/xxhash/v2](https://github.com/cespare/xxhash/blob/v2.3.0/LICENSE.txt) | v2.3.0 | MIT | See linked license |
| [github.com/charmbracelet/colorprofile](https://github.com/charmbracelet/colorprofile/blob/v0.4.3/LICENSE) | v0.4.3 | MIT | See linked license |
| [github.com/charmbracelet/ultraviolet](https://github.com/charmbracelet/ultraviolet/blob/6b8d4baf91b4/LICENSE) | v0.0.0-20261008173134-6b8d4baf91b4 | MIT | See linked license |
| [github.com/charmbracelet/x/ansi](https://github.com/charmbracelet/x/blob/ansi/v0.11.9/ansi/LICENSE) | v0.11.9 | MIT | See linked license |
| [github.com/charmbracelet/x/term](https://github.com/charmbracelet/x/blob/term/v0.2.2/term/LICENSE) | v0.2.2 | MIT | See linked license |
| [github.com/charmbracelet/x/termios](https://github.com/charmbracelet/x/blob/termios/v0.1.1/termios/LICENSE) | v0.1.1 | MIT | See linked license |
| [github.com/charmbracelet/x/windows](https://github.com/charmbracelet/x/blob/windows/v0.2.2/windows/LICENSE) | v0.2.2 | MIT | See linked license |
| [github.com/clbanning/mxj/v2](https://github.com/clbanning/mxj/blob/v2.7.0/LICENSE) | v2.7.0 | MIT | See linked license |
| [github.com/clipperhouse/displaywidth](https://github.com/clipperhouse/displaywidth/blob/v0.11.0/LICENSE) | v0.11.0 | MIT | See linked license |
| [github.com/clipperhouse/uax29/v2/graphemes](https://github.com/clipperhouse/uax29/blob/v2.7.0/LICENSE) | v2.7.0 | MIT | See linked license |
| [github.com/cloudflare/circl](https://github.com/cloudflare/circl/blob/v1.6.3/LICENSE) | v1.6.3 | BSD-3-Clause | See linked license |
| [github.com/coder/websocket](https://github.com/coder/websocket/blob/v1.8.12/LICENSE.txt) | v1.8.12 | ISC | See linked license |
| [github.com/cretz/bine](https://github.com/cretz/bine/blob/v0.2.0/LICENSE) | v0.2.0 | MIT | See linked license |
| [github.com/dnsimple/dnsimple-go/v10/dnsimple](https://github.com/dnsimple/dnsimple-go/blob/v10.0.0/LICENSE.txt) | v10.0.0 | MIT | See linked license |
| [github.com/dustin/go-humanize](https://github.com/dustin/go-humanize/blob/v1.1.0/LICENSE) | v1.1.0 | MIT | See linked license |
| [github.com/exoscale/egoscale/v3](https://github.com/exoscale/egoscale/blob/v3.1.63/v3/LICENSE) | v3.1.63 | Apache-2.0 | See linked license |
| [github.com/fatih/structs](https://github.com/fatih/structs/blob/v1.1.0/LICENSE) | v1.1.0 | MIT | See linked license |
| [github.com/felixge/httpsnoop](https://github.com/felixge/httpsnoop/blob/v1.1.0/LICENSE.txt) | v1.1.0 | MIT | See linked license |
| [github.com/fsnotify/fsnotify](https://github.com/fsnotify/fsnotify/blob/v1.10.1/LICENSE) | v1.10.1 | BSD-3-Clause | See linked license |
| [github.com/gabriel-vasile/mimetype](https://github.com/gabriel-vasile/mimetype/blob/v1.4.16/LICENSE) | v1.4.16 | MIT | See linked license |
| [github.com/ghodss/yaml](https://github.com/ghodss/yaml/blob/v1.0.0/LICENSE) | v1.0.0 | MIT | See linked license |
| [github.com/go-acme/alidns-20150109/v5/client](https://github.com/go-acme/alidns-20150109/blob/v5.6.1/LICENSE) | v5.6.1 | Apache-2.0 | See linked license |
| [github.com/go-acme/esa-20240910/v3/client](https://github.com/go-acme/esa-20240910/blob/v3.14.2/LICENSE) | v3.14.2 | Apache-2.0 | See linked license |
| [github.com/go-acme/jdcloud-sdk-go](https://github.com/go-acme/jdcloud-sdk-go/blob/v1.64.0/LICENSE) | v1.64.0 | Apache-2.0 | See linked license |
| [github.com/go-acme/jdcloud-sdk-go/services/domainservice/models](https://github.com/go-acme/jdcloud-sdk-go/blob/v1.64.0/services/domainservice/models/Notice.go) | v1.64.0 | Apache-2.0 | See linked license |
| [github.com/go-acme/lego/v5](https://github.com/go-acme/lego/blob/v5.7.0/LICENSE) | v5.7.0 | MIT | See linked license |
| [github.com/go-acme/tencentclouddnspod/v20210323](https://github.com/go-acme/tencentclouddnspod/blob/v1.3.131/LICENSE) | v1.3.131 | Apache-2.0 | See linked license |
| [github.com/go-acme/tencentedgdeone/v20220901](https://github.com/go-acme/tencentedgdeone/blob/v1.3.186/LICENSE) | v1.3.186 | Apache-2.0 | See linked license |
| [github.com/go-chi/chi/v5](https://github.com/go-chi/chi/blob/v5.3.0/LICENSE) | v5.3.0 | MIT | See linked license |
| [github.com/go-errors/errors](https://github.com/go-errors/errors/blob/v1.0.1/LICENSE.MIT) | v1.0.1 | MIT | See linked license |
| [github.com/go-jose/go-jose/v4](https://github.com/go-jose/go-jose/blob/v4.1.9/LICENSE) | v4.1.9 | Apache-2.0 | See linked license |
| [github.com/go-jose/go-jose/v4/json](https://github.com/go-jose/go-jose/blob/v4.1.9/json/LICENSE) | v4.1.9 | BSD-3-Clause | See linked license |
| [github.com/go-logr/logr](https://github.com/go-logr/logr/blob/v1.4.4/LICENSE) | v1.4.4 | Apache-2.0 | See linked license |
| [github.com/go-logr/stdr](https://github.com/go-logr/stdr/blob/v1.2.2/LICENSE) | v1.2.2 | Apache-2.0 | See linked license |
| [github.com/go-ozzo/ozzo-validation/v4](https://github.com/go-ozzo/ozzo-validation/blob/v4.5.0/LICENSE) | v4.5.0 | MIT | See linked license |
| [github.com/go-playground/locales](https://github.com/go-playground/locales/blob/v0.14.2/LICENSE) | v0.14.2 | MIT | See linked license |
| [github.com/go-playground/universal-translator](https://github.com/go-playground/universal-translator/blob/v0.18.2/LICENSE) | v0.18.2 | MIT | See linked license |
| [github.com/go-playground/validator/v10](https://github.com/go-playground/validator/blob/v10.30.6/LICENSE) | v10.30.6 | MIT | See linked license |
| [github.com/go-viper/mapstructure/v2](https://github.com/go-viper/mapstructure/blob/v2.5.0/LICENSE) | v2.5.0 | MIT | See linked license |
| [github.com/goccy/go-yaml](https://github.com/goccy/go-yaml/blob/v1.19.2/LICENSE) | v1.19.2 | MIT | See linked license |
| [github.com/gofrs/flock](https://github.com/gofrs/flock/blob/v0.13.2/LICENSE) | v0.13.2 | BSD-3-Clause | See linked license |
| [github.com/gofrs/uuid](https://github.com/gofrs/uuid/blob/v4.4.0/LICENSE) | v4.4.0+incompatible | MIT | See linked license |
| [github.com/gofrs/uuid/v5](https://github.com/gofrs/uuid/blob/v5.5.1/LICENSE) | v5.5.1 | MIT | See linked license |
| [github.com/golang-jwt/jwt/v4](https://github.com/golang-jwt/jwt/blob/v4.5.2/LICENSE) | v4.5.2 | MIT | See linked license |
| [github.com/golang-jwt/jwt/v5](https://github.com/golang-jwt/jwt/blob/v5.3.1/LICENSE) | v5.3.1 | MIT | See linked license |
| [github.com/golang/protobuf/proto](https://github.com/golang/protobuf/blob/v1.5.4/LICENSE) | v1.5.4 | BSD-3-Clause | See linked license |
| [github.com/google/go-querystring/query](https://github.com/google/go-querystring/blob/v1.2.0/LICENSE) | v1.2.0 | BSD-3-Clause | See linked license |
| [github.com/google/s2a-go](https://github.com/google/s2a-go/blob/v0.1.11/LICENSE.md) | v0.1.11 | Apache-2.0 | See linked license |
| [github.com/google/uuid](https://github.com/google/uuid/blob/v1.6.0/LICENSE) | v1.6.0 | BSD-3-Clause | See linked license |
| [github.com/googleapis/enterprise-certificate-proxy/client](https://github.com/googleapis/enterprise-certificate-proxy/blob/v0.3.25/LICENSE) | v0.3.25 | Apache-2.0 | See linked license |
| [github.com/googleapis/gax-go/v2](https://github.com/googleapis/gax-go/blob/v2.27.0/v2/LICENSE) | v2.27.0 | BSD-3-Clause | See linked license |
| [github.com/gophercloud/gophercloud](https://github.com/gophercloud/gophercloud/blob/v1.14.1/LICENSE) | v1.14.1 | Apache-2.0 | See linked license |
| [github.com/gophercloud/gophercloud/v2](https://github.com/gophercloud/gophercloud/blob/v2.16.0/LICENSE) | v2.16.0 | Apache-2.0 | See linked license |
| [github.com/gophercloud/utils/v2](https://github.com/gophercloud/utils/blob/b4759ea0529b/LICENSE) | v2.0.0-20260922174841-b4759ea0529b | Apache-2.0 | See linked license |
| [github.com/gorilla/css/scanner](https://github.com/gorilla/css/blob/v1.0.1/LICENSE) | v1.0.1 | BSD-3-Clause | See linked license |
| [github.com/gorilla/websocket](https://github.com/gorilla/websocket/blob/d67f41855da4/LICENSE) | v1.5.4-0.20240701034025-d67f41855da4 | BSD-2-Clause | See linked license |
| [github.com/graph-gophers/graphql-go](https://github.com/graph-gophers/graphql-go/blob/v1.10.2/LICENSE) | v1.10.2 | BSD-2-Clause | See linked license |
| [github.com/hashicorp/errwrap](https://github.com/hashicorp/errwrap/blob/v1.0.0/LICENSE) | v1.0.0 | MPL-2.0 | See linked license |
| [github.com/hashicorp/go-cleanhttp](https://github.com/hashicorp/go-cleanhttp/blob/v0.5.2/LICENSE) | v0.5.2 | MPL-2.0 | See linked license |
| [github.com/hashicorp/go-multierror](https://github.com/hashicorp/go-multierror/blob/v1.1.1/LICENSE) | v1.1.1 | MPL-2.0 | See linked license |
| [github.com/hashicorp/go-retryablehttp](https://github.com/hashicorp/go-retryablehttp/blob/v0.7.8/LICENSE) | v0.7.8 | MPL-2.0 | See linked license |
| [github.com/hashicorp/go-uuid](https://github.com/hashicorp/go-uuid/blob/v1.0.4/LICENSE) | v1.0.4 | MPL-2.0 | See linked license |
| [github.com/hashicorp/hcl](https://github.com/hashicorp/hcl/blob/v1.0.0/LICENSE) | v1.0.0 | MPL-2.0 | See linked license |
| [github.com/huaweicloud/huaweicloud-sdk-go-v3](https://github.com/huaweicloud/huaweicloud-sdk-go-v3/blob/v0.1.223/LICENSE) | v0.1.223 | BSD-2-Clause | See linked license |
| [github.com/infobloxopen/infoblox-go-client/v2](https://github.com/infobloxopen/infoblox-go-client/blob/v2.12.0/LICENSE) | v2.12.0 | Apache-2.0 | See linked license |
| [github.com/jcmturner/aescts/v2](https://github.com/jcmturner/aescts/blob/v2.0.0/v2/LICENSE) | v2.0.0 | Apache-2.0 | See linked license |
| [github.com/jcmturner/dnsutils/v2](https://github.com/jcmturner/dnsutils/blob/v2.0.0/v2/LICENSE) | v2.0.0 | Apache-2.0 | See linked license |
| [github.com/jcmturner/gofork](https://github.com/jcmturner/gofork/blob/v1.7.6/LICENSE) | v1.7.6 | BSD-3-Clause | See linked license |
| [github.com/jcmturner/goidentity/v6](https://github.com/jcmturner/goidentity/blob/v6.0.1/v6/LICENSE) | v6.0.1 | Apache-2.0 | See linked license |
| [github.com/jcmturner/gokrb5/v8](https://github.com/jcmturner/gokrb5/blob/v8.4.4/v8/LICENSE) | v8.4.4 | Apache-2.0 | See linked license |
| [github.com/jcmturner/rpc/v2](https://github.com/jcmturner/rpc/blob/v2.0.3/v2/LICENSE) | v2.0.3 | Apache-2.0 | See linked license |
| [github.com/json-iterator/go](https://github.com/json-iterator/go/blob/71ac16282d12/LICENSE) | v1.1.13-0.20220915233716-71ac16282d12 | MIT | See linked license |
| [github.com/k0kubun/go-ansi](https://github.com/k0kubun/go-ansi/blob/3bf9e2903213/LICENSE.txt) | v0.0.0-20180517002512-3bf9e2903213 | MIT | See linked license |
| [github.com/kolo/xmlrpc](https://github.com/kolo/xmlrpc/blob/a4b6fa1dd06b/LICENSE) | v0.0.0-20220921171641-a4b6fa1dd06b | MIT | See linked license |
| [github.com/kylelemons/godebug](https://github.com/kylelemons/godebug/blob/v1.1.0/LICENSE) | v1.1.0 | Apache-2.0 | See linked license |
| [github.com/labbsr0x/bindman-dns-webhook/src](https://github.com/labbsr0x/bindman-dns-webhook/blob/v1.0.2/LICENSE) | v1.0.2 | MIT | See linked license |
| [github.com/labbsr0x/goh/gohclient](https://github.com/labbsr0x/goh/blob/v1.0.1/LICENSE) | v1.0.1 | MIT | See linked license |
| [github.com/leodido/go-urn](https://github.com/leodido/go-urn/blob/v1.5.0/LICENSE) | v1.5.0 | MIT | See linked license |
| [github.com/linode/linodego/v2](https://github.com/linode/linodego/blob/v2.11.0/LICENSE) | v2.11.0 | MIT | See linked license |
| [github.com/liquidweb/liquidweb-cli](https://github.com/liquidweb/liquidweb-cli/blob/v0.6.10/LICENSE) | v0.6.10 | Apache-2.0 | See linked license |
| [github.com/liquidweb/liquidweb-go](https://github.com/liquidweb/liquidweb-go/blob/v1.6.4/LICENSE) | v1.6.4 | Apache-2.0 | See linked license |
| [github.com/lucasb-eyer/go-colorful](https://github.com/lucasb-eyer/go-colorful/blob/v1.4.1/LICENSE) | v1.4.1 | MIT | See linked license |
| [github.com/magiconair/properties](https://github.com/magiconair/properties/blob/v1.18.12/LICENSE.md) | v1.18.12 | BSD-2-Clause | See linked license |
| [github.com/mattn/go-runewidth](https://github.com/mattn/go-runewidth/blob/v0.0.31/LICENSE) | v0.0.31 | MIT | See linked license |
| [github.com/microcosm-cc/bluemonday](https://github.com/microcosm-cc/bluemonday/blob/v1.0.27/LICENSE.md) | v1.0.27 | BSD-3-Clause | See linked license |
| [github.com/miekg/dns](https://github.com/miekg/dns/blob/v1.1.73/LICENSE) | v1.1.73 | BSD-3-Clause | See linked license |
| [github.com/mimuret/golang-iij-dpf/pkg](https://github.com/mimuret/golang-iij-dpf/blob/v0.9.1/LICENSE) | v0.9.1 | MIT | See linked license |
| [github.com/mitchellh/go-homedir](https://github.com/mitchellh/go-homedir/blob/v1.1.0/LICENSE) | v1.1.0 | MIT | See linked license |
| [github.com/mitchellh/mapstructure](https://github.com/mitchellh/mapstructure/blob/v1.5.0/LICENSE) | v1.5.0 | MIT | See linked license |
| [github.com/modern-go/concurrent](https://github.com/modern-go/concurrent/blob/bacd9c7ef1dd/LICENSE) | v0.0.0-20180306012644-bacd9c7ef1dd | Apache-2.0 | See linked license |
| [github.com/modern-go/reflect2](https://github.com/modern-go/reflect2/blob/35a7c28c31ee/LICENSE) | v1.0.3-0.20250322232337-35a7c28c31ee | Apache-2.0 | See linked license |
| [github.com/muesli/cancelreader](https://github.com/muesli/cancelreader/blob/v0.2.2/LICENSE) | v0.2.2 | MIT | See linked license |
| [github.com/munnerz/goautoneg](https://github.com/munnerz/goautoneg/blob/a7dc8b61c822/LICENSE) | v0.0.0-20191010083416-a7dc8b61c822 | BSD-3-Clause | See linked license |
| [github.com/Myra-Security-GmbH/myrasec-go/v2](https://github.com/Myra-Security-GmbH/myrasec-go/blob/v2.63.0/LICENSE) | v2.63.0 | MIT | See linked license |
| [github.com/Myra-Security-GmbH/signature](https://github.com/Myra-Security-GmbH/signature/blob/v1.1.0/LICENSE) | v1.1.0 | MIT | See linked license |
| [github.com/namedotcom/go/v4/namecom](https://github.com/namedotcom/go/blob/v4.0.2/LICENSE) | v4.0.2 | MIT | See linked license |
| [github.com/nrdcg/auroradns](https://github.com/nrdcg/auroradns/blob/v1.2.0/LICENSE) | v1.2.0 | MPL-2.0 | See linked license |
| [github.com/nrdcg/bunny-go](https://github.com/nrdcg/bunny-go/blob/v0.1.0/LICENSE) | v0.1.0 | MIT | See linked license |
| [github.com/nrdcg/desec](https://github.com/nrdcg/desec/blob/v0.11.3/LICENSE) | v0.11.3 | MPL-2.0 | See linked license |
| [github.com/nrdcg/freemyip](https://github.com/nrdcg/freemyip/blob/v0.3.0/LICENSE) | v0.3.0 | MPL-2.0 | See linked license |
| [github.com/nrdcg/goacmedns](https://github.com/nrdcg/goacmedns/blob/v0.2.0/LICENSE) | v0.2.0 | MIT | See linked license |
| [github.com/nrdcg/goinwx](https://github.com/nrdcg/goinwx/blob/v0.12.0/LICENSE) | v0.12.0 | MIT | See linked license |
| [github.com/nrdcg/mailinabox](https://github.com/nrdcg/mailinabox/blob/v0.3.0/LICENSE) | v0.3.0 | MPL-2.0 | See linked license |
| [github.com/nrdcg/namesilo](https://github.com/nrdcg/namesilo/blob/v0.5.0/LICENSE) | v0.5.0 | MPL-2.0 | See linked license |
| [github.com/nrdcg/nodion](https://github.com/nrdcg/nodion/blob/v0.1.0/LICENSE) | v0.1.0 | MPL-2.0 | See linked license |
| [github.com/nrdcg/oci-go-sdk/common/v1065](https://github.com/nrdcg/oci-go-sdk/blob/common/v1065.131.0/common/LICENSE.txt) | v1065.131.0 | Apache-2.0 | See linked license |
| [github.com/nrdcg/oci-go-sdk/dns/v1065](https://github.com/nrdcg/oci-go-sdk/blob/dns/v1065.131.0/dns/LICENSE.txt) | v1065.131.0 | Apache-2.0 | See linked license |
| [github.com/nrdcg/porkbun](https://github.com/nrdcg/porkbun/blob/v0.4.0/LICENSE) | v0.4.0 | MPL-2.0 | See linked license |
| [github.com/nrdcg/vegadns](https://github.com/nrdcg/vegadns/blob/v0.3.0/LICENSE) | v0.3.0 | Apache-2.0 | See linked license |
| [github.com/nzdjb/go-metaname](https://github.com/nzdjb/go-metaname/blob/v1.0.0/LICENSE) | v1.0.0 | MIT | See linked license |
| [github.com/oschwald/maxminddb-golang](https://github.com/oschwald/maxminddb-golang/blob/v1.13.1/LICENSE) | v1.13.1 | ISC | See linked license |
| [github.com/ovh/go-ovh/ovh](https://github.com/ovh/go-ovh/blob/v1.10.0/LICENSE) | v1.10.0 | BSD-3-Clause | See linked license |
| [github.com/pelletier/go-toml/v2](https://github.com/pelletier/go-toml/blob/v2.4.3/LICENSE) | v2.4.3 | MIT | See linked license |
| [github.com/peterhellberg/link](https://github.com/peterhellberg/link/blob/v1.2.0/LICENSE) | v1.2.0 | MIT | See linked license |
| [github.com/pires/go-proxyproto](https://github.com/pires/go-proxyproto/blob/v0.14.0/LICENSE) | v0.14.0 | Apache-2.0 | See linked license |
| [github.com/pkg/browser](https://github.com/pkg/browser/blob/5ac0b6a4141c/LICENSE) | v0.0.0-20240102092130-5ac0b6a4141c | BSD-2-Clause | See linked license |
| [github.com/pkg/errors](https://github.com/pkg/errors/blob/v0.9.1/LICENSE) | v0.9.1 | BSD-2-Clause | See linked license |
| [github.com/pquerna/otp](https://github.com/pquerna/otp/blob/v1.5.0/LICENSE) | v1.5.0 | Apache-2.0 | See linked license |
| [github.com/prometheus/client_golang/internal/github.com/golang/gddo/httputil](https://github.com/prometheus/client_golang/blob/v1.25.0/internal/github.com/golang/gddo/LICENSE) | v1.25.0 | BSD-3-Clause | See linked license |
| [github.com/prometheus/client_golang/prometheus](https://github.com/prometheus/client_golang/blob/v1.25.0/LICENSE) | v1.25.0 | Apache-2.0 | See linked license |
| [github.com/prometheus/client_model/go](https://github.com/prometheus/client_model/blob/v0.6.3/LICENSE) | v0.6.3 | Apache-2.0 | See linked license |
| [github.com/prometheus/common](https://github.com/prometheus/common/blob/v0.72.0/LICENSE) | v0.72.0 | Apache-2.0 | See linked license |
| [github.com/prometheus/procfs](https://github.com/prometheus/procfs/blob/v0.22.0/LICENSE) | v0.22.0 | Apache-2.0 | See linked license |
| [github.com/ProtonMail/go-crypto](https://github.com/ProtonMail/go-crypto/blob/v1.5.2/LICENSE) | v1.5.2 | BSD-3-Clause | See linked license |
| [github.com/regfish/regfish-dnsapi-go](https://github.com/regfish/regfish-dnsapi-go/blob/v0.2.0/LICENSE) | v0.2.0 | MIT | See linked license |
| [github.com/remyoudompheng/bigfft](https://github.com/remyoudompheng/bigfft/blob/24d4a6f8daec/LICENSE) | v0.0.0-20230129092748-24d4a6f8daec | BSD-3-Clause | See linked license |
| [github.com/rivo/uniseg](https://github.com/rivo/uniseg/blob/3a370411ca06/LICENSE.txt) | v0.4.8-0.20261110175422-3a370411ca06 | MIT | See linked license |
| [github.com/sacloud/sacloud-sdk-go/api/iaas](https://github.com/sacloud/sacloud-sdk-go/blob/v0.7.0/api/iaas/LICENSE) | v0.7.0 | Apache-2.0 | See linked license |
| [github.com/sacloud/sacloud-sdk-go/api/iaas/naked](https://github.com/sacloud/sacloud-sdk-go/blob/v0.7.0/api/iaas/naked/license.go) | v0.7.0 | Apache-2.0 | See linked license |
| [github.com/sacloud/sacloud-sdk-go/common/packages](https://github.com/sacloud/sacloud-sdk-go/blob/v0.7.0/common/packages/LICENSE) | v0.7.0 | Apache-2.0 | See linked license |
| [github.com/sacloud/sacloud-sdk-go/common/saclient](https://github.com/sacloud/sacloud-sdk-go/blob/v0.7.0/common/saclient/LICENSE) | v0.7.0 | Apache-2.0 | See linked license |
| [github.com/sacloud/sacloud-sdk-go/internal/api-client](https://github.com/sacloud/sacloud-sdk-go/blob/v0.7.0/internal/api-client/LICENSE) | v0.7.0 | Apache-2.0 | See linked license |
| [github.com/sacloud/sacloud-sdk-go/internal/go-http](https://github.com/sacloud/sacloud-sdk-go/blob/v0.7.0/internal/go-http/LICENSE) | v0.7.0 | Apache-2.0 | See linked license |
| [github.com/sagikazarmark/slog-shim](https://github.com/sagikazarmark/slog-shim/blob/v0.1.0/LICENSE) | v0.1.0 | BSD-3-Clause | See linked license |
| [github.com/scaleway/scaleway-sdk-go](https://github.com/scaleway/scaleway-sdk-go/blob/v1.39.0/LICENSE) | v1.39.0 | Apache-2.0 | See linked license |
| [github.com/selectel/domains-go/pkg/v2](https://github.com/selectel/domains-go/blob/v1.1.0/LICENSE) | v1.1.0 | Apache-2.0 | See linked license |
| [github.com/selectel/go-selvpcclient/v5/selvpcclient](https://github.com/selectel/go-selvpcclient/blob/v5.0.0/LICENSE) | v5.0.0 | Apache-2.0 | See linked license |
| [github.com/shirou/gopsutil/v3](https://github.com/shirou/gopsutil/blob/v3.24.5/LICENSE) | v3.24.5 | BSD-3-Clause | See linked license |
| [github.com/shopspring/decimal](https://github.com/shopspring/decimal/blob/v1.4.0/LICENSE) | v1.4.0 | MIT | See linked license |
| [github.com/sirupsen/logrus](https://github.com/sirupsen/logrus/blob/v1.10.2/LICENSE) | v1.10.2 | MIT | See linked license |
| [github.com/softlayer/softlayer-go](https://github.com/softlayer/softlayer-go/blob/v1.2.1/LICENSE) | v1.2.1 | Apache-2.0 | See linked license |
| [github.com/softlayer/xmlrpc](https://github.com/softlayer/xmlrpc/blob/5f089df7cb7e/LICENSE) | v0.0.0-20200409220501-5f089df7cb7e | MIT | See linked license |
| [github.com/sony/gobreaker/v2](https://github.com/sony/gobreaker/blob/v2.4.0/v2/LICENSE) | v2.4.0 | MIT | See linked license |
| [github.com/spf13/afero](https://github.com/spf13/afero/blob/v1.15.0/LICENSE.txt) | v1.15.0 | Apache-2.0 | See linked license |
| [github.com/spf13/cast](https://github.com/spf13/cast/blob/v1.10.0/LICENSE) | v1.10.0 | MIT | See linked license |
| [github.com/spf13/pflag](https://github.com/spf13/pflag/blob/v1.0.10/LICENSE) | v1.0.10 | BSD-3-Clause | See linked license |
| [github.com/spf13/viper](https://github.com/spf13/viper/blob/v1.18.2/LICENSE) | v1.18.2 | MIT | See linked license |
| [github.com/stretchr/objx](https://github.com/stretchr/objx/blob/v0.5.3/LICENSE) | v0.5.3 | MIT | See linked license |
| [github.com/stretchr/testify](https://github.com/stretchr/testify/blob/v1.12.1/LICENSE) | v1.12.1 | MIT | See linked license |
| [github.com/stretchr/testify/internal/difflib](https://github.com/stretchr/testify/blob/v1.12.1/internal/difflib/LICENSE) | v1.12.1 | BSD-3-Clause | See linked license |
| [github.com/stretchr/testify/internal/spew](https://github.com/stretchr/testify/blob/v1.12.1/internal/spew/LICENSE) | v1.12.1 | ISC | See linked license |
| [github.com/subosito/gotenv](https://github.com/subosito/gotenv/blob/v1.6.0/LICENSE) | v1.6.0 | MIT | See linked license |
| [github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common](https://github.com/tencentcloud/tencentcloud-sdk-go/blob/tencentcloud/common/v1.3.213/tencentcloud/common/LICENSE) | v1.3.213 | Apache-2.0 | See linked license |
| [github.com/tjfoc/gmsm/sm3](https://github.com/tjfoc/gmsm/blob/v1.4.1/LICENSE) | v1.4.1 | Apache-2.0 | See linked license |
| [github.com/tklauser/go-sysconf](https://github.com/tklauser/go-sysconf/blob/v0.3.12/LICENSE) | v0.3.12 | BSD-3-Clause | See linked license |
| [github.com/tklauser/numcpus](https://github.com/tklauser/numcpus/blob/v0.6.1/LICENSE) | v0.6.1 | Apache-2.0 | See linked license |
| [github.com/transip/gotransip/v6](https://github.com/transip/gotransip/blob/v6.29.0/LICENSE) | v6.29.0 | MIT | See linked license |
| [github.com/tursodatabase/libsql-client-go](https://github.com/tursodatabase/libsql-client-go/blob/9d5d30a29a60/LICENSE) | v0.0.0-20260528064733-9d5d30a29a60 | MIT | See linked license |
| [github.com/ucloud/ucloud-sdk-go](https://github.com/ucloud/ucloud-sdk-go/blob/v0.22.150/LICENSE) | v0.22.150 | Apache-2.0 | See linked license |
| [github.com/ultradns/ultradns-go-sdk](https://github.com/ultradns/ultradns-go-sdk/blob/v1.8.6-20261009044533-535b847/LICENSE) | v1.8.6-20261009044533-535b847 | Apache-2.0 | See linked license |
| [github.com/vinyldns/go-vinyldns/vinyldns](https://github.com/vinyldns/go-vinyldns/blob/v0.9.18/LICENSE) | v0.9.18 | Apache-2.0 | See linked license |
| [github.com/volcengine/volc-sdk-golang](https://github.com/volcengine/volc-sdk-golang/blob/v1.0.259/LICENSE) | v1.0.259 | Apache-2.0 | See linked license |
| [github.com/vultr/govultr/v3](https://github.com/vultr/govultr/blob/v3.34.0/LICENSE) | v3.34.0 | MIT | See linked license |
| [github.com/xo/terminfo](https://github.com/xo/terminfo/blob/v1.0.0/LICENSE) | v1.0.0 | MIT | See linked license |
| [github.com/yandex-cloud/go-genproto/yandex/cloud](https://github.com/yandex-cloud/go-genproto/blob/v0.139.0/LICENSE) | v0.139.0 | MIT | See linked license |
| [github.com/yandex-cloud/go-sdk/services/dns/v1](https://github.com/yandex-cloud/go-sdk/blob/services/dns/v0.0.112/services/dns/LICENSE) | v0.0.112 | MIT | See linked license |
| [github.com/yandex-cloud/go-sdk/v2](https://github.com/yandex-cloud/go-sdk/blob/v2.191.0/LICENSE) | v2.191.0 | MIT | See linked license |
| [github.com/youmark/pkcs8](https://github.com/youmark/pkcs8/blob/a2c0da244d78/LICENSE) | v0.0.0-20240726163527-a2c0da244d78 | MIT | See linked license |
| [github.com/yuin/goldmark](https://github.com/yuin/goldmark/blob/v1.8.7/LICENSE) | v1.8.7 | MIT | See linked license |
| [Go source incorporated by modernc.org/libc](https://gitlab.com/cznic/libc/-/blob/v1.75.7/LICENSE-3RD-PARTY.md) | bundled in v1.75.7 | BSD-3-Clause | See linked license |
| [go-netdb incorporated by modernc.org/libc](https://gitlab.com/cznic/libc/-/blob/v1.75.7/LICENSE-3RD-PARTY.md) | bundled in v1.75.7 | MIT | See linked license |
| [go.mongodb.org/mongo-driver](https://github.com/mongodb/mongo-go-driver/blob/v1.17.10/LICENSE) | v1.17.10 | Apache-2.0 | See linked license |
| [go.opentelemetry.io/auto/sdk](https://github.com/open-telemetry/opentelemetry-go-instrumentation/blob/sdk/v1.2.1/sdk/LICENSE) | v1.2.1 | Apache-2.0 | See linked license |
| [go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp](https://github.com/open-telemetry/opentelemetry-go-contrib/blob/instrumentation/net/http/otelhttp/v0.69.0/instrumentation/net/http/otelhttp/LICENSE) | v0.69.0 | Apache-2.0 | See linked license |
| [go.opentelemetry.io/otel](https://github.com/open-telemetry/opentelemetry-go/blob/v1.47.0/LICENSE) | v1.47.0 | Apache-2.0 | See linked license |
| [go.opentelemetry.io/otel/log](https://github.com/open-telemetry/opentelemetry-go/blob/log/v1.47.0/log/LICENSE) | v1.47.0 | Apache-2.0 | See linked license |
| [go.opentelemetry.io/otel/metric](https://github.com/open-telemetry/opentelemetry-go/blob/metric/v1.47.0/metric/LICENSE) | v1.47.0 | Apache-2.0 | See linked license |
| [go.opentelemetry.io/otel/trace](https://github.com/open-telemetry/opentelemetry-go/blob/trace/v1.47.0/trace/LICENSE) | v1.47.0 | Apache-2.0 | See linked license |
| [go.uber.org/multierr](https://github.com/uber-go/multierr/blob/v1.11.0/LICENSE.txt) | v1.11.0 | MIT | See linked license |
| [go.uber.org/ratelimit](https://github.com/uber-go/ratelimit/blob/v0.3.1/LICENSE) | v0.3.1 | MIT | See linked license |
| [go.uber.org/zap](https://github.com/uber-go/zap/blob/v1.28.0/LICENSE) | v1.28.0 | MIT | See linked license |
| [go.yaml.in/yaml/v3](https://github.com/yaml/go-yaml/blob/v3.0.6/LICENSE) | v3.0.6 | MIT | See linked license |
| [golang.org/x/crypto](https://cs.opensource.google/go/x/crypto/+/v0.59.0:LICENSE) | v0.59.0 | BSD-3-Clause | See linked license |
| [golang.org/x/exp/slices](https://cs.opensource.google/go/x/exp/+/746e56fc:LICENSE) | v0.0.0-20260410095643-746e56fc9e2f | BSD-3-Clause | See linked license |
| [golang.org/x/image](https://cs.opensource.google/go/x/image/+/v0.45.0:LICENSE) | v0.45.0 | BSD-3-Clause | See linked license |
| [golang.org/x/net](https://cs.opensource.google/go/x/net/+/v0.63.0:LICENSE) | v0.63.0 | BSD-3-Clause | See linked license |
| [golang.org/x/oauth2](https://cs.opensource.google/go/x/oauth2/+/v0.38.0:LICENSE) | v0.38.0 | BSD-3-Clause | See linked license |
| [golang.org/x/sync](https://cs.opensource.google/go/x/sync/+/v0.25.0:LICENSE) | v0.25.0 | BSD-3-Clause | See linked license |
| [golang.org/x/sys](https://cs.opensource.google/go/x/sys/+/v0.50.0:LICENSE) | v0.50.0 | BSD-3-Clause | See linked license |
| [golang.org/x/term](https://cs.opensource.google/go/x/term/+/v0.48.0:LICENSE) | v0.48.0 | BSD-3-Clause | See linked license |
| [golang.org/x/text](https://cs.opensource.google/go/x/text/+/v0.44.0:LICENSE) | v0.44.0 | BSD-3-Clause | See linked license |
| [golang.org/x/time/rate](https://cs.opensource.google/go/x/time/+/v0.16.0:LICENSE) | v0.16.0 | BSD-3-Clause | See linked license |
| [google.golang.org/api](https://github.com/googleapis/google-api-go-client/blob/v0.316.0/LICENSE) | v0.316.0 | BSD-3-Clause | See linked license |
| [google.golang.org/api/internal/third_party/uritemplates](https://github.com/googleapis/google-api-go-client/blob/v0.316.0/internal/third_party/uritemplates/LICENSE) | v0.316.0 | BSD-3-Clause | See linked license |
| [google.golang.org/genproto/googleapis/api](https://github.com/googleapis/go-genproto/blob/4341edf3f941/googleapis/api/LICENSE) | v0.0.0-20261012220534-4341edf3f941 | Apache-2.0 | See linked license |
| [google.golang.org/genproto/googleapis/rpc](https://github.com/googleapis/go-genproto/blob/d1e00e9f33ab/googleapis/rpc/LICENSE) | v0.0.0-20261104224025-d1e00e9f33ab | Apache-2.0 | See linked license |
| [google.golang.org/grpc](https://github.com/grpc/grpc-go/blob/v1.85.0/LICENSE) | v1.85.0 | Apache-2.0 | See linked license |
| [google.golang.org/protobuf](https://github.com/protocolbuffers/protobuf-go/blob/v1.36.12/LICENSE) | v1.36.12 | BSD-3-Clause | See linked license |
| [gopkg.in/ini.v1](https://github.com/go-ini/ini/blob/v1.67.3/LICENSE) | v1.67.3 | Apache-2.0 | See linked license |
| [gopkg.in/ns1/ns1-go.v2/rest](https://github.com/ns1/ns1-go/blob/v2.18.0/LICENSE.txt) | v2.18.0 | Apache-2.0 | See linked license |
| [gopkg.in/yaml.v2](https://github.com/go-yaml/yaml/blob/v2.4.0/LICENSE) | v2.4.0 | Apache-2.0 | See linked license |
| [gopkg.in/yaml.v3](https://github.com/go-yaml/yaml/blob/v3.0.1/LICENSE) | v3.0.1 | MIT | See linked license |
| [GraphiQL](https://github.com/graphql/graphiql/blob/graphiql%404.1.2/LICENSE) | v4.1.2 | MIT | See linked license |
| [modernc.org/libc](https://gitlab.com/cznic/libc/blob/v1.75.7/LICENSE-3RD-PARTY.md) | v1.75.7 | MIT | See linked license |
| [modernc.org/mathutil](https://gitlab.com/cznic/mathutil/-/blob/v1.7.1/LICENSE) | v1.7.1 | BSD-3-Clause | See linked license |
| [modernc.org/memory](https://gitlab.com/cznic/memory/blob/v1.12.1/LICENSE-GO) | v1.12.1 | BSD-3-Clause | See linked license |
| [modernc.org/sqlite](https://gitlab.com/cznic/sqlite/blob/v1.59.0/LICENSE) | v1.59.0 | BSD-3-Clause | See linked license |
| [musl libc sources incorporated by modernc.org/libc](https://gitlab.com/cznic/libc/-/blob/v1.75.7/LICENSE-3RD-PARTY.md) | bundled in v1.75.7 | MIT | See linked license |
| [NixOS/nixpkgs code incorporated by modernc.org/libc](https://gitlab.com/cznic/libc/-/blob/v1.75.7/LICENSE-3RD-PARTY.md) | bundled in v1.75.7 | MIT | See linked license |
| [React](https://github.com/facebook/react/blob/v18.3.1/LICENSE) | v18.3.1 | MIT | See linked license |
| [ReactDOM](https://github.com/facebook/react/blob/v18.3.1/LICENSE) | v18.3.1 | MIT | See linked license |
| [software.sslmate.com/src/go-pkcs12](https://github.com/SSLMate/go-pkcs12/blob/v0.7.4/LICENSE) | v0.7.4 | BSD-3-Clause | See linked license |
| [SQLite source incorporated by modernc.org/sqlite](https://gitlab.com/cznic/sqlite/-/blob/v1.59.0/LICENSE) | bundled in v1.59.0 | Public Domain | See linked license |
| [Swagger UI](https://github.com/swagger-api/swagger-ui/blob/v5.36.0/LICENSE) | v5.36.0 | Apache-2.0 | See linked license |

The `modernc.org/libc` and `modernc.org/sqlite` dependencies include source-file components with separate notices; their full attribution is preserved in the linked upstream license files above.

The locally bundled Swagger UI, GraphiQL, React, and ReactDOM license texts are also included beside their assets under `src/server/static/vendor/`.

BSD-3-Clause: neither the names of the copyright holders nor the names of their contributors may be used to endorse or promote products derived from this software without specific prior written permission.

Full license texts: https://spdx.org/licenses/

---
