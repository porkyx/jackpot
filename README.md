# Jackpot

DCInside 게시글의 댓글로 즉시·예약 추첨을 진행하고, 결과를 텍스트와 PNG로 공유하는 Windows 앱입니다.

## 사용하기

Windows 11 x64와 Microsoft WebView2 Runtime이 필요합니다.

1. 릴리즈 ZIP을 풀고 `Jackpot.exe`를 실행합니다.
2. 게시글 주소를 불러와 참가자·필터·상품을 설정합니다.
3. 추첨 후 결과를 복사하거나 PNG로 저장합니다.

예약은 앱이 실행 중일 때 수행합니다. 종료·절전 중 지나간 예약은 다음 실행에서 복구합니다.

## 기여하기

개발 환경: Go 1.26.4, Node.js 22.16.0, pnpm 12.9.1.

프로젝트 루트에서 의존성을 설치하고 빌드합니다.

```powershell
pnpm --dir frontend install --frozen-lockfile
./scripts/build-release.ps1
```

빌드 결과는 `dist/`에 생성됩니다. 변경 후 다음 명령으로 검증합니다.

```powershell
./scripts/pre-commit.ps1
```

타사 라이선스는 [THIRD-PARTY-NOTICES.md](third_party/THIRD-PARTY-NOTICES.md)를 참고하세요.
