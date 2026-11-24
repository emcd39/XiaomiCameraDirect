"""Standalone local login probe for the xiaomi_direct_pilot account flow.

Loads account.py directly from disk (no Home Assistant import) and drives the
real XiaomiAccount.login() / start_qr() / finish_qr() implementation. Every HTTP
request and every response body is printed with secrets redacted, so you can
see exactly which stage fails on a real account.

Examples (run from the repository root):

    python scripts/ha_direct_login_probe.py --mock success
    python scripts/ha_direct_login_probe.py --mock phone --code 123456 --region cn
    python scripts/ha_direct_login_probe.py --region cn --method password --username ME
    python scripts/ha_direct_login_probe.py --region cn --method qr

Credentials are read from the terminal, never from argv. Nothing is written to
disk and no token is printed.
"""

import argparse
import asyncio
import getpass
import importlib.util
import json
from pathlib import Path
import sys
from types import SimpleNamespace

ROOT = Path(__file__).resolve().parents[1]
ACCOUNT_PATH = ROOT / "custom_components" / "xiaomi_direct_pilot" / "xiaomi_direct" / "account.py"


def load_account():
    """Import account.py by path, bypassing the HA-only package __init__."""
    spec = importlib.util.spec_from_file_location("xiaomi_account_local", ACCOUNT_PATH)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


SECRET_HINTS = (
    "token", "password", "security", "secret", "session", "cookie",
    "sign", "nonce", "hash", "identity", "userid", "cuserid", "did",
)


def sanitize(value, key=""):
    lowered = key.lower()
    if any(hint in lowered for hint in SECRET_HINTS):
        return "<redacted>"
    if isinstance(value, dict):
        return {k: sanitize(v, k) for k, v in value.items()}
    if isinstance(value, list):
        return [sanitize(item, key) for item in value[:20]]
    if isinstance(value, str) and len(value) > 160:
        return value[:160] + "...(truncated)"
    return value


def format_body(raw):
    if raw.startswith(b"&&&START&&&"):
        raw = raw[len(b"&&&START&&&"):]
    try:
        data = json.loads(raw)
    except (ValueError, UnicodeDecodeError):
        return f"<non-json {len(raw)} bytes>"
    return json.dumps(sanitize(data), ensure_ascii=False, separators=(",", ":"))[:2000]


class TraceClient:
    """Wrap a real or mock client to log each request in order."""

    def __init__(self, inner):
        self._inner = inner
        self.count = 0

    def get(self, url, **kwargs):
        self.count += 1
        print(f"\n[REQ {self.count}] GET {url}")
        return self._inner.get(url, **kwargs)

    def post(self, url, **kwargs):
        self.count += 1
        print(f"\n[REQ {self.count}] POST {url}")
        return self._inner.post(url, **kwargs)


class MockResponse:
    def __init__(self, body=b"", *, cookies=None, status=200, headers=None):
        self.body = body
        self.cookies = {k: SimpleNamespace(value=v) for k, v in (cookies or {}).items()}
        self.status = status
        self.headers = headers or {}
        self.content = self

    async def __aenter__(self):
        return self

    async def __aexit__(self, *_):
        pass

    async def iter_chunked(self, _size):
        yield self.body

    def raise_for_status(self):
        pass


def reply(**fields):
    return b"&&&START&&&" + json.dumps(fields).encode()


class MockClient:
    """Canned Xiaomi responses for offline verification of the probe itself."""

    def __init__(self, scenario):
        self.scenario = scenario

    def get(self, url, **_kwargs):
        url = str(url)
        if "/pass/serviceLogin?" in url:
            return MockResponse(reply(qs="q", _sign="s", sid="xiaomiio", callback="cb"))
        if url.endswith("/identity/list"):
            flag = "2" if self.scenario == "deadend" else "4"
            return MockResponse(reply(flag=flag), cookies={"identity_session": "transient"})
        if "/identity/auth/verifyPhone" in url:
            return MockResponse(reply(maskedPhone="138****0000"))
        if url.startswith("https://api.io.mi.com/auth"):
            return MockResponse(cookies={"userId": "uid", "passToken": "saved", "serviceToken": "session"})
        raise AssertionError(f"unexpected GET {url}")

    def post(self, url, **kwargs):
        url = str(url)
        if url.endswith("/pass/serviceLoginAuth2"):
            if self.scenario == "captcha":
                return MockResponse(reply(captchaUrl="https://account.xiaomi.com/captcha"))
            if self.scenario in ("phone", "deadend"):
                return MockResponse(reply(
                    notificationUrl="https://account.xiaomi.com/fe/service/identity/authStart",
                    ssecurity="c2VjcmV0", passToken="saved",
                ))
            return MockResponse(reply(location="https://api.io.mi.com/auth", ssecurity="c2VjcmV0", passToken="saved"))
        if url.endswith("/sendPhoneTicket"):
            return MockResponse(reply(code=0))
        if "/identity/auth/verifyPhone" in url:
            assert kwargs.get("cookies", {}).get("identity_session") == "transient"
            return MockResponse(reply(location="https://api.io.mi.com/auth"))
        raise AssertionError(f"unexpected POST {url}")


def describe_challenge(acc, error):
    print(f"\n=== Xiaomi challenge: {error.code} ===")
    if error.code == "captcha_required":
        print("A graphical captcha is required. Open the captchaURL shown above in a browser;")
        print("this local probe cannot solve it for you.")
    elif error.code == "verification_required":
        if getattr(error, "continuation", None):
            print("A phone/email code was requested. The continuation is available.")
        else:
            print("DEAD END: Xiaomi asked for a verification method this project cannot complete.")
            print("Likely cause: identity flag was not 4 (phone) / 8 (email), or identity_session cookie was missing.")
    elif error.code == "qr_pending":
        print("QR has not been scanned/approved yet.")
    elif error.code == "qr_expired":
        print("QR expired; restart the probe.")


async def list_cameras(acc, client, region, mock):
    if mock:
        async def fake_devices(self):
            return {"list": [
                {"did": "mock-029a02", "name": "小米智能摄像机 云台版2K", "model": "chuangmi.camera.029a02"},
                {"did": "mock-069a01", "name": "小米智能摄像机3 云台版", "model": "chuangmi.camera.069a01"},
            ]}
        acc.XiaomiAccount.devices = fake_devices
    print("\n--- device inventory ---")
    try:
        devices = await acc.XiaomiAccount(client, region=region, user_id="uid", pass_token="saved").devices()
    except acc.AccountError as error:
        print(f"inventory failed: {error}")
        return
    items = devices.get("list", []) if isinstance(devices, dict) else devices
    if not items:
        print("no cameras returned")
        return
    for item in items:
        print(f"  {item.get('model', '?')}  {item.get('name', '?')}  did={item.get('did', '?')}")


async def run(args, acc):
    inner = MockClient(args.mock) if args.mock else acc.create_client_session()
    client = TraceClient(inner)

    # The module-level _body() is called by every stage; wrap it to dump responses.
    original_body = acc._body

    async def traced_body(response):
        raw = await original_body(response)
        print(f"    <- {format_body(raw)}")
        return raw

    acc._body = traced_body

    try:
        if args.method == "qr":
            approval = await acc.XiaomiAccount.start_qr(client)
            print(f"\nScan with Mi Home: {approval.page}")
            print(f"QR image: {approval.image}")
            account = None
            for attempt in range(15):
                try:
                    account = await acc.XiaomiAccount.finish_qr(client, approval, args.region)
                    break
                except acc.AccountChallenge as error:
                    describe_challenge(acc, error)
                    if error.code == "qr_expired":
                        return 3
                    print(f"  waiting for approval ({attempt + 1}/15)...")
                    await asyncio.sleep(2)
            if account is None:
                print("QR was not approved in time.")
                return 3
        else:
            username = args.username or ("mock-user" if args.mock else input("Mi account (email/phone): ").strip())
            password = args.password if args.password is not None else (
                "mock-password" if args.mock else getpass.getpass("Password: ")
            )
            try:
                account = await acc.XiaomiAccount.login(client, username, password, args.region)
            except acc.AccountChallenge as error:
                describe_challenge(acc, error)
                continuation = getattr(error, "continuation", None)
                if error.code == "verification_required" and continuation:
                    code = args.code or input("Verification code: ").strip()
                    account = await continuation(client, code)
                else:
                    return 3
            except acc.AccountError as error:
                print(f"\nLogin failed: {error}")
                return 2

        print(f"\nLogin OK: user_id={account.user_id}  pass_token=<obtained, {len(account.pass_token)} chars>")
        await list_cameras(acc, client, args.region, args.mock)
        return 0
    finally:
        acc._body = original_body
        close = getattr(inner, "close", None)
        if close:
            await close()


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--region", default="cn", choices=("de", "cn", "i2", "ru", "sg", "us"))
    parser.add_argument("--method", default="password", choices=("password", "qr"))
    parser.add_argument("--username", help="Optional; prompted securely if omitted")
    parser.add_argument("--password", help="Optional; prompted securely if omitted (discouraged on argv)")
    parser.add_argument("--code", help="Verification code, only for offline mock runs")
    parser.add_argument("--mock", choices=("success", "phone", "deadend", "captcha"),
                        help="Use canned responses instead of contacting Xiaomi")
    args = parser.parse_args()
    return asyncio.run(run(args, load_account()))


if __name__ == "__main__":
    raise SystemExit(main())
