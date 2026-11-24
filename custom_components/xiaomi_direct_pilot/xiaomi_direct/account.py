"""Restore an authorized Xiaomi account and request a signed MISS session."""

import asyncio
import base64
from dataclasses import dataclass, field
from hashlib import md5, sha1, sha256
import json
import secrets
import struct
import time

from aiohttp import (
    ClientError,
    ClientResponseError,
    ClientSession,
    ClientTimeout,
    DummyCookieJar,
    TCPConnector,
)
from aiohttp.resolver import ThreadedResolver
from cryptography.hazmat.decrepit.ciphers.algorithms import ARC4
from cryptography.hazmat.primitives.ciphers import Cipher
from yarl import URL


_LOGIN = URL("https://account.xiaomi.com/pass/serviceLogin?_json=true&sid=xiaomiio")
_QR_START = URL("https://account.xiaomi.com/longPolling/loginUrl")
_REGIONS = frozenset(("cn", "de", "i2", "ru", "sg", "us"))
_REQUEST = "/v2/device/miss_get_vendor"
_DEVICE_LIST = "/v2/home/device_list_page"
_MIOT_GET = "/miotspec/prop/get"
_C201_DIAGNOSTICS = {
    (2, 1): ("camera_enabled", None),
    (2, 3): ("night_shot", 255),
    (4, 1): ("indicator_light", None),
    (5, 1): ("motion_detection", None),
    (8, 6): ("human_detection", None),
    (8, 3): ("pet_detection", None),
    (3, 1): ("sd_card_status", 100),
    (3, 2): ("sd_card_total", 1 << 31),
    (3, 3): ("sd_card_free", 1 << 31),
    (3, 4): ("sd_card_used", 1 << 31),
}
_MAX_RESPONSE = 128 * 1024


def create_client_session():
    """Create an account client that uses the operating system DNS resolver.

    aiohttp's optional c-ares resolver can fail to resolve Xiaomi account
    hosts on Windows Home Assistant installs even when Windows DNS works.
    """
    return ClientSession(
        cookie_jar=DummyCookieJar(),
        connector=TCPConnector(resolver=ThreadedResolver()),
    )


class AccountError(Exception):
    """The authorized account could not provide a session."""


class AccountAuthorizationError(AccountError):
    """Stored authorization was rejected and the account must sign in again."""


class AccountChallenge(AccountError):
    """Sign-in requires an interactive captcha or second factor."""

    def __init__(self, code, continuation=None, captcha=None):
        self.code = code
        self.continuation = continuation
        self.captcha = captcha
        super().__init__(code)


@dataclass(frozen=True, slots=True)
class QRApproval:
    image: URL = field(repr=False)
    page: URL = field(repr=False)
    poll: URL = field(repr=False)
    expires: float


def _b64(data):
    return base64.b64encode(data).decode("ascii")


def _login_reply(raw, stage):
    prefix = b"&&&START&&&"
    if not raw.startswith(prefix):
        raise AccountError(f"Xiaomi returned an invalid response during {stage}")
    reply = json.loads(raw[len(prefix):])
    if not isinstance(reply, dict):
        raise AccountError(f"Xiaomi returned an invalid response during {stage}")
    return reply


def _response_code(reply):
    return reply.get("code") if type(reply.get("code")) is int else "missing"


def _requires_captcha(reply):
    return bool(reply.get("captchaURL") or reply.get("captchaUrl"))


def _rc4(key, body):
    cipher = Cipher(ARC4(key), mode=None).encryptor()
    cipher.update(bytes(1024))
    return cipher.update(body) + cipher.finalize()


def _signature(path, params, key):
    material = "&".join(("POST", path, *(f"{k}={v}" for k, v in params.items()), _b64(key)))
    return _b64(sha1(material.encode()).digest())


def _encode_request(path, values, security, nonce):
    key = sha256(security + nonce).digest()
    params = {"data": json.dumps(values, separators=(",", ":"))}
    params["rc4_hash__"] = _signature(path, params, key)
    encrypted = {name: _b64(_rc4(key, value.encode())) for name, value in params.items()}
    encrypted["signature"] = _signature(path, encrypted, key)
    encrypted["_nonce"] = _b64(nonce)
    return key, encrypted


def _xiaomi_https(url):
    return (
        url.scheme == "https" and url.port == 443 and not (url.user or url.password)
        and url.host is not None and (url.host == "mi.com" or url.host.endswith(".mi.com")
                                     or url.host == "xiaomi.com" or url.host.endswith(".xiaomi.com"))
    )


async def _captcha_challenge(client, reply):
    """Fetch the captcha image and its ick cookie (go2rtc cloud.go:getCaptcha)."""
    raw = reply.get("captchaURL") or reply.get("captchaUrl") or ""
    url = URL(raw)
    if url.host is None:
        url = URL("https://account.xiaomi.com" + (raw if raw.startswith("/") else "/" + raw))
    if not _xiaomi_https(url):
        raise AccountError("Xiaomi returned an invalid captcha URL")
    async with client.get(url, allow_redirects=False, timeout=ClientTimeout(total=10)) as response:
        response.raise_for_status()
        ick = response.cookies.get("ick")
        image = await _body(response)
    if not ick:
        raise AccountError("Xiaomi captcha did not provide a session cookie")
    return image, ick.value


async def _body(response):
    body = bytearray()
    async for chunk in response.content.iter_chunked(8192):
        body.extend(chunk)
        if len(body) > _MAX_RESPONSE:
            raise AccountError("Xiaomi account response exceeded limit")
    return bytes(body)


class XiaomiAccount:
    def __init__(self, client, *, region, user_id, pass_token):
        if region not in _REGIONS:
            raise AccountError("unknown Xiaomi account region")
        self._client = client
        self._user_id = user_id
        self._pass_token = pass_token
        self._host = "api.io.mi.com" if region == "cn" else f"{region}.api.io.mi.com"
        self._session = None
        self._lock = asyncio.Lock()

    @property
    def user_id(self):
        return self._user_id

    @property
    def pass_token(self):
        return self._pass_token

    @staticmethod
    async def start_qr(client):
        try:
            async with client.get(_LOGIN, allow_redirects=False, timeout=ClientTimeout(total=8)) as response:
                response.raise_for_status()
                initial = _login_reply(await _body(response), "qr_login_start")
            login_page = URL(initial.get("location") or "")
            if not _xiaomi_https(login_page):
                raise AccountError("Xiaomi did not provide a valid QR sign-in page")
            params = dict(login_page.query)
            params.update({"_qrsize": "240", "_hasLogo": "false", "_dc": str(int(time.time() * 1000))})
            async with client.get(
                _QR_START.with_query(params), allow_redirects=False, timeout=ClientTimeout(total=8),
            ) as response:
                response.raise_for_status()
                raw = await _body(response)
            reply = json.loads(raw.removeprefix(b"&&&START&&&"))
            image, page, poll = (URL(reply[key]) for key in ("qr", "loginUrl", "lp"))
            if reply.get("code") != 0 or not all(_xiaomi_https(url) for url in (image, page, poll)):
                raise AccountError("Xiaomi did not issue a usable QR challenge")
            return QRApproval(image, page, poll, time.monotonic() + 120)
        except (ClientError, TimeoutError, ValueError, TypeError, KeyError, AttributeError):
            raise AccountError("Xiaomi QR sign-in could not be started") from None

    @classmethod
    async def finish_qr(cls, client, approval, region):
        if time.monotonic() >= approval.expires:
            raise AccountChallenge("qr_expired")
        try:
            async with client.get(
                approval.poll, allow_redirects=False, timeout=ClientTimeout(total=8),
            ) as response:
                response.raise_for_status()
                raw = await _body(response)
            reply = json.loads(raw.removeprefix(b"&&&START&&&"))
            if reply.get("code") != 0 or not reply.get("location"):
                raise AccountChallenge("qr_pending")
            location = URL(reply["location"])
            if not _xiaomi_https(location):
                raise AccountError("Xiaomi QR sign-in returned an invalid callback")
            return await cls._finish_password_login(client, location, reply, region)
        except TimeoutError:
            raise AccountChallenge("qr_pending") from None
        except (ClientError, ValueError, TypeError, KeyError, AttributeError):
            raise AccountError("Xiaomi QR sign-in could not be completed") from None

    @classmethod
    async def login(cls, client, username, password, region):
        """Sign in with Mi account credentials and return a reusable account."""
        if region not in _REGIONS:
            raise AccountError("unknown Xiaomi account region")
        stage = "login_start"
        try:
            async with client.get(_LOGIN, allow_redirects=False, timeout=ClientTimeout(total=10)) as response:
                response.raise_for_status()
                initial = _login_reply(await _body(response), stage)
            form = {
                "_json": "true", "hash": md5(password.encode()).hexdigest().upper(),
                "sid": initial["sid"], "callback": initial["callback"],
                "_sign": initial["_sign"], "qs": initial["qs"], "user": username,
            }
            stage = "password_submit"
            async with client.post(
                URL("https://account.xiaomi.com/pass/serviceLoginAuth2"), data=form,
                cookies={"deviceId": secrets.token_urlsafe(12)}, allow_redirects=False,
                timeout=ClientTimeout(total=10),
            ) as response:
                response.raise_for_status()
                raw = await _body(response)
            result = _login_reply(raw, stage)
            if _requires_captcha(result):
                raise AccountChallenge("captcha_required")
            if result.get("notificationUrl"):
                stage = "identity_methods"
                notify = URL(result["notificationUrl"])
                if not _xiaomi_https(notify):
                    raise AccountError("Xiaomi sign-in was rejected")
                identity_url = URL(str(notify).replace("/fe/service/identity/authStart", "/identity/list"))
                async with client.get(identity_url, allow_redirects=False, timeout=ClientTimeout(total=10)) as response:
                    response.raise_for_status()
                    identity = _login_reply(await _body(response), stage)
                    identity_cookie = response.cookies.get("identity_session")
                    identity_session = identity_cookie.value if identity_cookie else ""
                flag = str(identity.get("flag", ""))
                endpoint = {"4": "Phone", "8": "Email"}.get(flag)
                if not endpoint or not identity_session:
                    raise AccountChallenge("verification_required")
                preflight_url = URL(f"https://account.xiaomi.com/identity/auth/verify{endpoint}").with_query(
                    {"_flag": flag, "_json": "true"}
                )
                ticket_url = URL(f"https://account.xiaomi.com/identity/auth/send{endpoint}Ticket")

                async def send_ticket(http_client, icode, ick):
                    stage = "verification_preflight"
                    async with http_client.get(preflight_url, cookies={"identity_session": identity_session},
                                               allow_redirects=False, timeout=ClientTimeout(total=10)) as response:
                        response.raise_for_status()
                        _login_reply(await _body(response), stage)
                    cookies = {"identity_session": identity_session}
                    if ick:
                        cookies["ick"] = ick
                    stage = "verification_send"
                    async with http_client.post(ticket_url, data={"_json": "true", "icode": icode, "retry": "0"},
                                                cookies=cookies, allow_redirects=False,
                                                timeout=ClientTimeout(total=10)) as response:
                        response.raise_for_status()
                        return _login_reply(await _body(response), stage)

                async def continue_verification(http_client, code):
                    try:
                        verify_url = URL(f"https://account.xiaomi.com/identity/auth/verify{endpoint}").with_query(
                            {"_flag": flag, "ticket": code, "trust": "false", "_json": "true"}
                        )
                        async with http_client.post(verify_url, cookies={"identity_session": identity_session},
                                                    allow_redirects=False, timeout=ClientTimeout(total=10)) as response:
                            response.raise_for_status()
                            verified = _login_reply(await _body(response), "verification_submit")
                        verified_location = URL(verified.get("location") or "")
                        if not _xiaomi_https(verified_location):
                            raise AccountError(f"Xiaomi rejected the verification code (code {_response_code(verified)})")
                        return await cls._finish_password_login(http_client, verified_location, result, region)
                    except AccountError:
                        raise
                    except (ClientError, TimeoutError, ValueError, TypeError, KeyError, AttributeError):
                        raise AccountError("Xiaomi verification failed") from None

                # go2rtc cloud.go:sendTicket - a captcha here must be solved and
                # replayed with icode+ick before the SMS/email ticket is issued.
                async def continue_captcha(http_client, captcha_code, ick):
                    ticket_result = await send_ticket(http_client, captcha_code, ick)
                    if _requires_captcha(ticket_result):
                        image, new_ick = await _captcha_challenge(http_client, ticket_result)
                        raise AccountChallenge(
                            "captcha_required",
                            lambda client, answer: continue_captcha(client, answer, new_ick),
                            image,
                        )
                    if ticket_result.get("code") not in (None, 0):
                        raise AccountError(f"Xiaomi rejected the verification request (code {_response_code(ticket_result)})")
                    raise AccountChallenge("verification_required", continue_verification)

                ticket_result = await send_ticket(client, "", "")
                if _requires_captcha(ticket_result):
                    image, ick = await _captcha_challenge(client, ticket_result)
                    raise AccountChallenge(
                        "captcha_required",
                        lambda http_client, answer: continue_captcha(http_client, answer, ick),
                        image,
                    )
                if ticket_result.get("code") not in (None, 0):
                    raise AccountError(f"Xiaomi rejected the verification request (code {_response_code(ticket_result)})")

                raise AccountChallenge("verification_required", continue_verification)
            location = URL(result.get("location") or "")
            if not _xiaomi_https(location):
                raise AccountError(f"Xiaomi rejected sign-in (code {_response_code(result)})")
            stage = "session_redirect"
            return await cls._finish_password_login(client, location, result, region)
        except AccountError:
            raise
        except ClientResponseError as error:
            raise AccountError(f"Xiaomi returned HTTP {error.status} during {stage}") from None
        except (ClientError, TimeoutError, ValueError, TypeError, KeyError, AttributeError) as error:
            raise AccountError(f"Xiaomi sign-in failed during {stage} ({type(error).__name__})") from None

    @classmethod
    async def _finish_password_login(cls, client, location, result, region):
        security = result.get("ssecurity")
        pass_token = result.get("passToken")
        cookies = {}
        async with asyncio.timeout(15):
            for _ in range(5):
                if not _xiaomi_https(location):
                    raise AccountError("Xiaomi sign-in was rejected")
                async with client.get(location, allow_redirects=False, timeout=ClientTimeout(total=10)) as response:
                    for name in ("userId", "cUserId", "passToken", "serviceToken"):
                        if name in response.cookies:
                            cookies[name] = response.cookies[name].value
                    if pragma := response.headers.get("Extension-Pragma"):
                        security = json.loads(pragma).get("ssecurity") or security
                    if response.status in (301, 302, 303, 307, 308):
                        location = location.join(URL(response.headers.get("Location", "")))
                        continue
                    response.raise_for_status()
                    break
            else:
                raise AccountError("Xiaomi sign-in exceeded redirect limit")
        user_id = cookies.get("userId") or str(result.get("userId") or "")
        pass_token = cookies.get("passToken") or pass_token
        if not user_id or not pass_token or not security or not cookies.get("serviceToken"):
            raise AccountError("Xiaomi sign-in was rejected")
        account = cls(client, region=region, user_id=user_id, pass_token=pass_token)
        cookies.setdefault("userId", user_id)
        if result.get("cUserId"):
            cookies.setdefault("cUserId", str(result["cUserId"]))
        cookies.pop("passToken", None)
        account._session = (base64.b64decode(security, validate=True), cookies)
        return account

    async def devices(self):
        return await self.request(_DEVICE_LIST, {})

    async def _restore(self):
        async with self._client.get(
            _LOGIN, cookies={"userId": self._user_id, "passToken": self._pass_token},
            allow_redirects=False, timeout=ClientTimeout(total=10),
        ) as response:
            if response.status in (401, 403):
                raise AccountAuthorizationError("Xiaomi sign-in requires renewed authorization")
            response.raise_for_status()
            body = await _body(response)
        if not body.startswith(b"&&&START&&&"):
            raise AccountError("Xiaomi sign-in response was invalid")
        login = json.loads(body[len(b"&&&START&&&"):])
        location = URL(login.get("location") or "")
        security = login.get("ssecurity")
        account_cookies = {}
        async with asyncio.timeout(15):
            for _ in range(5):
                if not _xiaomi_https(location):
                    raise AccountAuthorizationError("Xiaomi sign-in requires renewed authorization")
                async with self._client.get(location, allow_redirects=False) as response:
                    for name in ("userId", "cUserId", "serviceToken"):
                        if name in response.cookies:
                            account_cookies[name] = response.cookies[name].value
                    if pragma := response.headers.get("Extension-Pragma"):
                        security = json.loads(pragma).get("ssecurity") or security
                    if response.status in (301, 302, 303, 307, 308):
                        location = location.join(URL(response.headers.get("Location", "")))
                        continue
                    response.raise_for_status()
                    break
            else:
                raise AccountError("Xiaomi sign-in exceeded redirect limit")
        if not security or not account_cookies.get("serviceToken"):
            raise AccountAuthorizationError("Xiaomi sign-in requires renewed authorization")
        account_cookies.setdefault("userId", self._user_id)
        if account_cookies["userId"] != self._user_id:
            raise AccountAuthorizationError("Xiaomi sign-in returned a different account")
        return base64.b64decode(security, validate=True), account_cookies

    async def request(self, path, values):
        if path not in (_REQUEST, _DEVICE_LIST, _MIOT_GET):
            raise AccountError("unsupported Xiaomi account request")
        try:
            async with self._lock:
                if self._session is None:
                    self._session = await self._restore()
                session = self._session
            security, cookies = session
            nonce = secrets.token_bytes(8) + struct.pack(">I", int(time.time() // 60))
            key, form = _encode_request(path, values, security, nonce)
            async with self._client.post(
                f"https://{self._host}/app{path}", cookies=cookies, data=form,
                allow_redirects=False, timeout=ClientTimeout(total=10),
            ) as response:
                if response.status in (401, 403) and self._session is session:
                    self._session = None
                if response.status != 200:
                    raise AccountError("Xiaomi rejected the signed session request")
                body = await _body(response)
            reply = json.loads(_rc4(key, base64.b64decode(body, validate=True)))
        except (ClientError, TimeoutError, ValueError, TypeError, AttributeError) as error:
            raise AccountError(f"Xiaomi account request failed ({type(error).__name__})") from None
        result = reply.get("result") if isinstance(reply, dict) else None
        result_types = (dict, list) if path == _MIOT_GET else (dict,)
        if not isinstance(result, result_types) or reply.get("code") != 0:
            raise AccountError("Xiaomi account request was rejected")
        return result

    async def read_c201_settings(self, device_id):
        """Read C201 settings and raw SD diagnostics in one MIoT request."""
        result = await self.request(_MIOT_GET, {"params": [
            {"did": device_id, "siid": siid, "piid": piid}
            for siid, piid in _C201_DIAGNOSTICS
        ]})
        rows = result.get("result") if isinstance(result, dict) else result
        if not isinstance(rows, list):
            raise AccountError("Xiaomi settings response is malformed")
        settings = {}
        for row in rows:
            if not isinstance(row, dict) or type(row.get("code")) is not int or row["code"] != 0:
                continue
            if row.get("did", device_id) != device_id:
                continue
            siid, piid = row.get("siid"), row.get("piid")
            if type(siid) is not int or type(piid) is not int:
                continue
            property_info = _C201_DIAGNOSTICS.get((siid, piid))
            if property_info is None:
                continue
            key, maximum = property_info
            value = row.get("value")
            if maximum is None and type(value) is bool:
                settings[key] = value
            elif maximum is not None and type(value) is int and 0 <= value <= maximum:
                settings[key] = value
        return settings
