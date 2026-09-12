# 3x-ui Deplexo Stack

نسخه‌ی بازطراحی‌شده برای اجرای 3x-ui روی Deplexo/PaaSهایی که یک ورودی عمومی HTTP/HTTPS دارند.
این پروژه از تجربه‌ی نسخه‌ی قبلی ساخته شده؛ نسخه‌ی قبلی برای یک پورت عمومی TLS از reverse proxy مسیرمحور استفاده می‌کرد و WS را روی `/xvpnws/` به پورت داخلی Xray می‌فرستاد.

## مسیرها و پورت‌های داخلی

| Transport | Path / Service prefix | Internal port | وضعیت روی Deplexo |
|---|---:|---:|---|
| VLESS + WebSocket | `/xvpnws/` | `20868` | پیشنهادی / پایدار |
| VLESS + XHTTP | `/xhttp/` | `20869` | پیشنهادی / پایدار |
| VLESS + gRPC | service name `grpc` → path prefix `/grpc/` | `20870` | آزمایشی؛ ingress باید HTTP/2 را حفظ کند |
| VLESS + HTTPUpgrade | `/httpupgrade/` | `20871` | قابل تست |
| Subscription | `/sub/` | `2096` | رزرو شده؛ برای inbound استفاده نکن |
| 3x-ui panel | سایر مسیرها | `20530` | داخلی |

پورت عمومی برنامه `2053` است، مگر اینکه Deplexo متغیر `PORT` دیگری بدهد.

## چرا نصب قبلی خراب می‌شد و این نسخه چه فرقی دارد؟

در نسخه‌ی قدیمی، `main.go` در صورت نبودن فایل `/app/x-ui/x-ui` سعی می‌کرد release رسمی را **هنگام runtime** دانلود و extract کند. روی filesystem محدود/read-only Deplexo همین مرحله می‌توانست با خطای نبودن `/app/x-ui/x-ui` شکست بخورد.

در این نسخه 3x-ui فقط در **Docker build** دانلود می‌شود. Build با retry زیاد، timeout مشخص، نسخه‌ی pin شده و fallback به latest stable انجام می‌شود و قبل از ادامه وجود `x-ui/x-ui` داخل tar و روی filesystem بررسی می‌شود. اگر دانلود واقعاً شکست بخورد، image اصلاً ناقص Deploy نمی‌شود.

## مشکل read-only و Xray Update

- دیتابیس/log پیش‌فرض در `/tmp` قرار می‌گیرند، چون image layer در Deplexo read-only است.
- `XUI_BIN_FOLDER=/tmp/xui-bin` است.
- فایل‌های بزرگ `geoip/geosite/xray` در startup از image به `/tmp` **symlink** می‌شوند تا فضای writable پر نشود.
- یک permission watcher روی `/tmp/xui-bin` فعال است. اگر دکمه‌ی **Update Xray** در پنل symlink را با فایل جدید جایگزین کند، watcher فوراً روی `xray*` مجوز `0755` می‌گذارد تا خطای `permission denied` قبلی تکرار نشود.

> آپدیت خود **پنل 3x-ui** از داخل UI روی image read-only توصیه نمی‌شود. برای پنل از روش نسخه‌ای/Build زیر استفاده کن.

## آپدیت امن

نسخه‌های فعلی در دو فایل کوچک هستند:

- `XUI_VERSION`
- `CLOUDFLARED_VERSION`

در GitHub به **Actions → Update upstream versions → Run workflow** برو. Workflow آخرین stable release هر دو پروژه را می‌گیرد، فایل نسخه را تغییر می‌دهد و commit می‌کند. تغییر فایل نسخه Docker cache را هم می‌شکند، بنابراین Build بعدی واقعاً باینری جدید را دانلود می‌کند.

اگر Deplexo روی commitهای GitHub Auto Deploy دارد، قبل از فعال‌کردن آن بخش «دیتابیس پایدار» را بخوان.

## دیتابیس پایدار برای اینکه Redeploy تنظیمات را پاک نکند

حالت پیش‌فرض SQLite روی `/tmp` است و با ساخت کانتینر جدید ممکن است پاک شود. برای استفاده‌ی جدی یکی از این دو راه را انتخاب کن:

1. اگر Deplexo Persistent Volume می‌دهد، `XUI_DB_FOLDER` را روی آن mount تنظیم کن.
2. بهتر برای PaaS: PostgreSQL بیرونی. در Environment Variables:

```text
XUI_DB_TYPE=postgres
XUI_DB_DSN=postgres://USER:PASSWORD@HOST:5432/DBNAME?sslmode=require
```

3x-ui جدید PostgreSQL را به‌صورت رسمی پشتیبانی می‌کند. بدون persistence، قبل از Redeploy از دیتابیس بکاپ بگیر.

## تنظیم Deplexo

- Build method: `Dockerfile`
- Root directory: `./`
- Public/App port: `2053`
- `deplexo.yaml` لازم نیست.

Environment اختیاری:

```text
CLOUDFLARE_TUNNEL_TOKEN=...
```

وجود این متغیر Cloudflare Tunnel را همزمان با مسیر عادی Deplexo بالا می‌آورد. نبودنش هیچ اثری روی پنل/کانفیگ‌ها ندارد.

## Cloudflare Tunnel بکاپ

بعد از Connected شدن Tunnel، یک Published Application مثل این بساز:

```text
backup.example.com -> http://127.0.0.1:2053
```

مسیر عادی Deplexo/Custom Domain و Tunnel می‌توانند همزمان فعال بمانند. Tunnel فقط بکاپ ingress است؛ اگر خود کانتینر Deplexo Down شود، Tunnel داخل همان کانتینر هم Down می‌شود.

## تنظیم inboundها

### WebSocket

```text
Protocol: VLESS
Port: 20868
Transport: WebSocket
Path: /xvpnws/
Host: خالی
Security: None
Flow: خالی
```

سمت client TLS روی دامنه‌ی عمومی/Cloudflare روشن است و SNI همان دامنه است.

### XHTTP

```text
Protocol: VLESS
Port: 20869
Transport: XHTTP
Path: /xhttp/
Security: None
Flow: خالی
```

Mode را با `auto` شروع کن؛ بعداً بر اساس شبکه `packet-up` یا `stream-one` را A/B تست کن.

### gRPC

```text
Protocol: VLESS
Port: 20870
Transport: gRPC
Service Name: grpc
Security: None
```

پروکسی Go درخواست‌های `/grpc/...` را با h2c به Xray می‌فرستد. اگر ingress خود Deplexo HTTP/2 را تا کانتینر حفظ نکند، gRPC ممکن است با وجود درست بودن کد کار نکند. در این حالت XHTTP را نگه دار.

### HTTPUpgrade

```text
Protocol: VLESS
Port: 20871
Transport: HTTPUpgrade
Path: /httpupgrade/
Host: خالی
Security: None
```

## سرعت و latency

در مسیر داخلی از connection pooling و keep-alive بزرگ استفاده شده و buffer pool برای کاهش allocation/GC وجود دارد. روی WS هیچ `FlushInterval=-1` اجباری نشده تا رفتار نسخه‌ی سریع قبلی تغییر نکند. با این حال سرعت اصلی را route اپراتور، Cloudflare/Deplexo node، محدودیت CPU/RAM/Bandwidth هاست و congestion تعیین می‌کنند؛ هیچ گزینه‌ی کدی نمی‌تواند پهنای‌باند واقعی هاست را نامحدود کند.

برای مقایسه‌ی واقعی WS/XHTTP، هر بار با همان شبکه و همان endpoint چند تست دانلود/آپلود و latency بگیر، نه فقط ping داخلی v2rayNG.

## چه چیزهایی را نمی‌شود با ingress فعلی Deplexo عمومی کرد؟

این router بر اساس HTTP Path کار می‌کند. بنابراین transportهای HTTP-friendly مثل WebSocket/XHTTP/gRPC/HTTPUpgrade قابل route هستند.

اما برای این‌ها ingress جدا لازم است:

- REALITY + RAW/TCP: نیاز به TCP passthrough و ClientHello اصلی دارد.
- Hysteria/Hysteria2: نیاز به UDP/QUIC عمومی دارد.
- mKCP/QUIC/TUIC: نیاز به UDP عمومی دارند.

اگر Deplexo در آینده Raw TCP/UDP port بدهد، این پروتکل‌ها را می‌توان **مستقیم** روی آن پورت‌ها منتشر کرد؛ نباید از reverse proxy HTTP فعلی عبورشان داد.

## امنیت

UUIDها، دیتابیس، subscription links، Cloudflare Tunnel token و credentialهای PostgreSQL را داخل GitHub commit نکن.
