# Third-Party Notices

Jackpot 0.1.0 Windows x64 release candidate — 2026-10-07.

This package contains the third-party components listed below. Their license and notice files are provided verbatim in `licenses/`. These third-party licenses do not assign a license to Jackpot itself.

The Go module inventory was read from the actual release executable with `go version -m`, then checked against cached `go list -m -json` metadata with network lookup disabled. All 16 linked module names, versions, and module sums matched. Go runtime/standard-library notices, the production frontend packages, and the embedded font are also included. Development and test packages are not part of this inventory.

The published `@wailsio/runtime` package contains no standalone LICENSE file. It declares MIT and belongs to the same Wails repository and version as the backend; the original Wails MIT LICENSE is provided for that component. Additional upstream notices are retained as supplied, including files that also describe upstream components beyond the code used by this application. Wails example/template fonts, development tools, test fixtures, and platform SDKs are not packaged.

## Components

| Component | Version | Source | License/notice files |
| --- | --- | --- | --- |
| `github.com/adrg/xdg` | `v0.5.3` | [upstream](https://github.com/adrg/xdg) | [licenses/go/github.com_adrg_xdg@v0.5.3/LICENSE](licenses/go/github.com_adrg_xdg@v0.5.3/LICENSE) |
| `github.com/clipperhouse/uax29/v2` | `v2.7.0` | [upstream](https://github.com/clipperhouse/uax29) | [licenses/go/github.com_clipperhouse_uax29_v2@v2.7.0/LICENSE](licenses/go/github.com_clipperhouse_uax29_v2@v2.7.0/LICENSE) |
| `github.com/dustin/go-humanize` | `v1.0.1` | [upstream](https://github.com/dustin/go-humanize) | [licenses/go/github.com_dustin_go-humanize@v1.0.1/LICENSE](licenses/go/github.com_dustin_go-humanize@v1.0.1/LICENSE) |
| `github.com/go-ole/go-ole` | `v1.3.0` | [upstream](https://github.com/go-ole/go-ole) | [licenses/go/github.com_go-ole_go-ole@v1.3.0/LICENSE](licenses/go/github.com_go-ole_go-ole@v1.3.0/LICENSE) |
| `github.com/google/uuid` | `v1.6.0` | [upstream](https://github.com/google/uuid) | [licenses/go/github.com_google_uuid@v1.6.0/LICENSE](licenses/go/github.com_google_uuid@v1.6.0/LICENSE) |
| `github.com/mattn/go-isatty` | `v0.0.24` | [upstream](https://github.com/mattn/go-isatty) | [licenses/go/github.com_mattn_go-isatty@v0.0.24/LICENSE](licenses/go/github.com_mattn_go-isatty@v0.0.24/LICENSE) |
| `github.com/ncruces/go-strftime` | `v1.0.0` | [upstream](https://github.com/ncruces/go-strftime) | [licenses/go/github.com_ncruces_go-strftime@v1.0.0/LICENSE](licenses/go/github.com_ncruces_go-strftime@v1.0.0/LICENSE) |
| `github.com/remyoudompheng/bigfft` | `v0.0.0-20230129092748-24d4a6f8daec` | [upstream](https://github.com/remyoudompheng/bigfft) | [licenses/go/github.com_remyoudompheng_bigfft@v0.0.0-20230129092748-24d4a6f8daec/LICENSE](licenses/go/github.com_remyoudompheng_bigfft@v0.0.0-20230129092748-24d4a6f8daec/LICENSE) |
| `github.com/wailsapp/go-webview2` | `v1.0.22` | [upstream](https://github.com/wailsapp/go-webview2) | [licenses/go/github.com_wailsapp_go-webview2@v1.0.22/LICENSE](licenses/go/github.com_wailsapp_go-webview2@v1.0.22/LICENSE)<br>[licenses/go/github.com_wailsapp_go-webview2@v1.0.22/webviewloader/LICENSE](licenses/go/github.com_wailsapp_go-webview2@v1.0.22/webviewloader/LICENSE) |
| `github.com/wailsapp/wails/v3` | `v3.0.0-beta.28` | [upstream](https://github.com/wailsapp/wails) | [licenses/go/github.com_wailsapp_wails_v3@v3.0.0-beta.28/LICENSE](licenses/go/github.com_wailsapp_wails_v3@v3.0.0-beta.28/LICENSE)<br>[licenses/go/github.com_wailsapp_wails_v3@v3.0.0-beta.28/internal/webview2/webviewloader/LICENSE](licenses/go/github.com_wailsapp_wails_v3@v3.0.0-beta.28/internal/webview2/webviewloader/LICENSE)<br>[licenses/go/github.com_wailsapp_wails_v3@v3.0.0-beta.28/internal/go-common-file-dialog/LICENSE](licenses/go/github.com_wailsapp_wails_v3@v3.0.0-beta.28/internal/go-common-file-dialog/LICENSE) |
| `golang.org/x/net` | `v0.59.0` | [upstream](https://go.googlesource.com/net) | [licenses/go/golang.org_x_net@v0.59.0/LICENSE](licenses/go/golang.org_x_net@v0.59.0/LICENSE) |
| `golang.org/x/sys` | `v0.48.0` | [upstream](https://go.googlesource.com/sys) | [licenses/go/golang.org_x_sys@v0.48.0/LICENSE](licenses/go/golang.org_x_sys@v0.48.0/LICENSE) |
| `modernc.org/libc` | `v1.77.1` | [upstream](https://modernc.org/libc) | [licenses/go/modernc.org_libc@v1.77.1/LICENSE](licenses/go/modernc.org_libc@v1.77.1/LICENSE)<br>[licenses/go/modernc.org_libc@v1.77.1/LICENSE-3RD-PARTY.md](licenses/go/modernc.org_libc@v1.77.1/LICENSE-3RD-PARTY.md) |
| `modernc.org/mathutil` | `v1.7.1` | [upstream](https://modernc.org/mathutil) | [licenses/go/modernc.org_mathutil@v1.7.1/LICENSE](licenses/go/modernc.org_mathutil@v1.7.1/LICENSE)<br>[licenses/go/modernc.org_mathutil@v1.7.1/mersenne/LICENSE](licenses/go/modernc.org_mathutil@v1.7.1/mersenne/LICENSE) |
| `modernc.org/memory` | `v1.12.1` | [upstream](https://modernc.org/memory) | [licenses/go/modernc.org_memory@v1.12.1/LICENSE](licenses/go/modernc.org_memory@v1.12.1/LICENSE)<br>[licenses/go/modernc.org_memory@v1.12.1/LICENSE-GO](licenses/go/modernc.org_memory@v1.12.1/LICENSE-GO)<br>[licenses/go/modernc.org_memory@v1.12.1/LICENSE-LOGO](licenses/go/modernc.org_memory@v1.12.1/LICENSE-LOGO)<br>[licenses/go/modernc.org_memory@v1.12.1/LICENSE-MMAP-GO](licenses/go/modernc.org_memory@v1.12.1/LICENSE-MMAP-GO) |
| `modernc.org/sqlite` | `v1.60.1` | [upstream](https://modernc.org/sqlite) | [licenses/go/modernc.org_sqlite@v1.60.1/LICENSE](licenses/go/modernc.org_sqlite@v1.60.1/LICENSE)<br>[licenses/go/modernc.org_sqlite@v1.60.1/LICENSE-3RD-PARTY.md](licenses/go/modernc.org_sqlite@v1.60.1/LICENSE-3RD-PARTY.md)<br>[licenses/go/modernc.org_sqlite@v1.60.1/LICENSE-SQLITE](licenses/go/modernc.org_sqlite@v1.60.1/LICENSE-SQLITE)<br>[licenses/go/modernc.org_sqlite@v1.60.1/LICENSE-SQLITE_VEC](licenses/go/modernc.org_sqlite@v1.60.1/LICENSE-SQLITE_VEC) |
| `Go runtime and standard library` | `go1.26.4` | [upstream](https://go.googlesource.com/go/+/refs/tags/go1.26.4) | [licenses/go-runtime/go1.26.4/LICENSE](licenses/go-runtime/go1.26.4/LICENSE)<br>[licenses/go-runtime/go1.26.4/PATENTS](licenses/go-runtime/go1.26.4/PATENTS)<br>[licenses/go-runtime/go1.26.4/src/vendor/golang.org/x/crypto/LICENSE](licenses/go-runtime/go1.26.4/src/vendor/golang.org/x/crypto/LICENSE)<br>[licenses/go-runtime/go1.26.4/src/vendor/golang.org/x/net/LICENSE](licenses/go-runtime/go1.26.4/src/vendor/golang.org/x/net/LICENSE)<br>[licenses/go-runtime/go1.26.4/src/vendor/golang.org/x/sys/LICENSE](licenses/go-runtime/go1.26.4/src/vendor/golang.org/x/sys/LICENSE)<br>[licenses/go-runtime/go1.26.4/src/vendor/golang.org/x/text/LICENSE](licenses/go-runtime/go1.26.4/src/vendor/golang.org/x/text/LICENSE) |
| `effect` | `4.0.1` | [upstream](https://github.com/Effect-TS/effect) | [licenses/frontend/effect-4.0.1/LICENSE](licenses/frontend/effect-4.0.1/LICENSE) |
| `@wailsio/runtime` | `3.0.0-beta.28` | [upstream](https://github.com/wailsapp/wails/tree/v3.0.0-beta.28/v3/internal/runtime/desktop/@wailsio/runtime) | [licenses/frontend/wailsio-runtime-3.0.0-beta.28/LICENSE](licenses/frontend/wailsio-runtime-3.0.0-beta.28/LICENSE) |
| `Pretendard Variable` | `1.3.9` | [upstream](https://github.com/orioncactus/pretendard/tree/v1.3.9) | [licenses/fonts/Pretendard-1.3.9/OFL.txt](licenses/fonts/Pretendard-1.3.9/OFL.txt) |

## Original-file verification

Every copied file was verified byte-for-byte by SHA-256 against its local original. Files are not reformatted, normalized, or abbreviated. SHA-256 values below identify the packaged originals.

| Relative path | Bytes | SHA-256 |
| --- | ---: | --- |
| `licenses/go/github.com_adrg_xdg@v0.5.3/LICENSE` | 1107 | `FB119FABDE05E05B70B07F445B3F41863B22326C0EBB5B7BEE9F0309112D3640` |
| `licenses/go/github.com_clipperhouse_uax29_v2@v2.7.0/LICENSE` | 1069 | `07E9E3DAACCC707C9B7D535ED1405A65D37919FDD42197FE55E68820E974688C` |
| `licenses/go/github.com_dustin_go-humanize@v1.0.1/LICENSE` | 1136 | `A973B4498C13EB74BAA2A8E5C351426A6826F2FCDD909916DBE53EE2E755FD71` |
| `licenses/go/github.com_go-ole_go-ole@v1.3.0/LICENSE` | 1119 | `D883DBCA28769FF23DD4368A8EDB7AD9F9F23A8F86ADDE2875C6E41A828AAC62` |
| `licenses/go/github.com_google_uuid@v1.6.0/LICENSE` | 1480 | `0A8D61ED3CBFD5312326E8126C31CE9C627A283ADC99131B56896D29ADA04B2D` |
| `licenses/go/github.com_mattn_go-isatty@v0.0.24/LICENSE` | 1099 | `08EAB1118C80885FA1FA6A6DD7303F65A379FCB3733E063D20D1BBC2C76E6FA1` |
| `licenses/go/github.com_ncruces_go-strftime@v1.0.0/LICENSE` | 1068 | `38AE43959DAF953A393A585B2988672CB65A5A541ACA0D0BE5E72595A0A16883` |
| `licenses/go/github.com_remyoudompheng_bigfft@v0.0.0-20230129092748-24d4a6f8daec/LICENSE` | 1479 | `DD26A7ABDDD02E2D0ABA97805B31F248EF7835D9E10DA289B22E3B8AB78B324D` |
| `licenses/go/github.com_wailsapp_go-webview2@v1.0.22/LICENSE` | 1117 | `3F10F3374CF30076196EB239667CE6063C64E4275AFD2B397F6343F69676BB79` |
| `licenses/go/github.com_wailsapp_go-webview2@v1.0.22/webviewloader/LICENSE` | 794 | `E2C9290019954C3C3664428B73B34186B2BB8FAAD9B2C50605D7CDD9DF5C7177` |
| `licenses/go/github.com_wailsapp_wails_v3@v3.0.0-beta.28/LICENSE` | 1076 | `CEDE95C28E6E9D67D1C1E49E8BA3C20250C7D4080C3FACA1579B6732327AE40D` |
| `licenses/go/github.com_wailsapp_wails_v3@v3.0.0-beta.28/internal/webview2/webviewloader/LICENSE` | 794 | `E2C9290019954C3C3664428B73B34186B2BB8FAAD9B2C50605D7CDD9DF5C7177` |
| `licenses/go/github.com_wailsapp_wails_v3@v3.0.0-beta.28/internal/go-common-file-dialog/LICENSE` | 1071 | `2C09E92358414EF4DA9739026ACADFBD0663118E590238B306D70CCF60DB3159` |
| `licenses/go/golang.org_x_net@v0.59.0/LICENSE` | 1453 | `911F8F5782931320F5B8D1160A76365B83AEA6447EE6C04FA6D5591467DB9DAD` |
| `licenses/go/golang.org_x_sys@v0.48.0/LICENSE` | 1453 | `911F8F5782931320F5B8D1160A76365B83AEA6447EE6C04FA6D5591467DB9DAD` |
| `licenses/go/modernc.org_libc@v1.77.1/LICENSE` | 1482 | `95FF867EB55A56935FA7492406CFA953FB7C13CA73F4C0A86AE05756B4605600` |
| `licenses/go/modernc.org_libc@v1.77.1/LICENSE-3RD-PARTY.md` | 10505 | `F597097EFE3D97021F89170746BD3A0FB9A8B6FB26B82043ED68A4E0283BEE6C` |
| `licenses/go/modernc.org_mathutil@v1.7.1/LICENSE` | 1486 | `BFA9BF72A72CA009FD62A8F84FCA3DCA67E51D93AF96352723646599898B6CF5` |
| `licenses/go/modernc.org_mathutil@v1.7.1/mersenne/LICENSE` | 1486 | `C05630F9CA899EA094620D37DAEA6BA66F99ADBF9268EA5FEA9DB607E1102BD9` |
| `licenses/go/modernc.org_memory@v1.12.1/LICENSE` | 1484 | `59895E669F48F168B6B858358F6005779CDF40A265F7828813061B56AF67B496` |
| `licenses/go/modernc.org_memory@v1.12.1/LICENSE-GO` | 1479 | `2D36597F7117C38B006835AE7F537487207D8EC407AA9D9980794B2030CBC067` |
| `licenses/go/modernc.org_memory@v1.12.1/LICENSE-LOGO` | 62 | `5AE5BEE3072A841376451B48D8CFCEC7188E10543926D5870828D36C8A750DC5` |
| `licenses/go/modernc.org_memory@v1.12.1/LICENSE-MMAP-GO` | 1518 | `C2EBA69F20D05414538C3A5DF7694DDE392E065FF70882E1625E90F5D6659FFF` |
| `licenses/go/modernc.org_sqlite@v1.60.1/LICENSE` | 1489 | `C6FE05491A60AE13BCD223088D2705E36DEDE24E5587226231D2459ADA5C4822` |
| `licenses/go/modernc.org_sqlite@v1.60.1/LICENSE-3RD-PARTY.md` | 74323 | `B4366B9C27A9364633E015011A231913ED19BF1228F335BF617E50A6F7B168A6` |
| `licenses/go/modernc.org_sqlite@v1.60.1/LICENSE-SQLITE` | 1506 | `8438C9C89B849131EAD81D5435CB97FCF052DF5B0B286DDA8A2D4C29E6CB3FD0` |
| `licenses/go/modernc.org_sqlite@v1.60.1/LICENSE-SQLITE_VEC` | 1068 | `6CE72BBE12D975BD5286E5AB0A064C069693300C47BCCBC57BEC18485F1621EA` |
| `licenses/go-runtime/go1.26.4/LICENSE` | 1453 | `911F8F5782931320F5B8D1160A76365B83AEA6447EE6C04FA6D5591467DB9DAD` |
| `licenses/go-runtime/go1.26.4/PATENTS` | 1303 | `96F408BFAE65BF137FC2525D3ECB030271C50C1E90799F87ABF8846D8DD505CC` |
| `licenses/go-runtime/go1.26.4/src/vendor/golang.org/x/crypto/LICENSE` | 1453 | `911F8F5782931320F5B8D1160A76365B83AEA6447EE6C04FA6D5591467DB9DAD` |
| `licenses/go-runtime/go1.26.4/src/vendor/golang.org/x/net/LICENSE` | 1453 | `911F8F5782931320F5B8D1160A76365B83AEA6447EE6C04FA6D5591467DB9DAD` |
| `licenses/go-runtime/go1.26.4/src/vendor/golang.org/x/sys/LICENSE` | 1453 | `911F8F5782931320F5B8D1160A76365B83AEA6447EE6C04FA6D5591467DB9DAD` |
| `licenses/go-runtime/go1.26.4/src/vendor/golang.org/x/text/LICENSE` | 1453 | `911F8F5782931320F5B8D1160A76365B83AEA6447EE6C04FA6D5591467DB9DAD` |
| `licenses/frontend/effect-4.0.1/LICENSE` | 1083 | `774C3BC5924AD8AE6C5A75F1C53DB13FEB238ADE15989625C513D07B60DEDF30` |
| `licenses/frontend/wailsio-runtime-3.0.0-beta.28/LICENSE` | 1076 | `CEDE95C28E6E9D67D1C1E49E8BA3C20250C7D4080C3FACA1579B6732327AE40D` |
| `licenses/fonts/Pretendard-1.3.9/OFL.txt` | 4418 | `D31DDD9F2BED32FD7E302A205CF2380BA0DE6529152D239EF99CFB6F261BFC04` |
