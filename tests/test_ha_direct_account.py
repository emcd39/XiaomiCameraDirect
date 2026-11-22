"""Verify account restore and MISS request framing without contacting Xiaomi."""

import base64
from hashlib import sha256
import json
from types import SimpleNamespace
import unittest

from aiohttp.resolver import ThreadedResolver
from yarl import URL

from custom_components.xiaomi_direct_pilot.xiaomi_direct import account


class Response:
    def __init__(self, body=b"", *, headers=None, cookies=None):
        self.headers = headers or {}
        self.cookies = {name: SimpleNamespace(value=value) for name, value in (cookies or {}).items()}
        self.status = 200
        self.body = body
        self.content = self

    async def __aenter__(self):
        return self

    async def __aexit__(self, *_):
        pass

    async def iter_chunked(self, _size):
        yield self.body

    def raise_for_status(self):
        pass


class AccountTest(unittest.IsolatedAsyncioTestCase):
    async def test_account_client_uses_system_dns_resolver(self):
        client = account.create_client_session()
        try:
            self.assertIsInstance(client.connector._resolver, ThreadedResolver)
        finally:
            await client.close()

    async def test_qr_approval_uses_xiaomi_hosts_and_creates_account(self):
        security = base64.b64encode(b"mock-security").decode()
        test = self

        class Client:
            def get(self, endpoint, **kwargs):
                test.assertFalse(kwargs["allow_redirects"])
                url = URL(endpoint)
                if url.path == "/pass/serviceLogin":
                    return Response(b'&&&START&&&{"location":"https://account.xiaomi.com/fe/service/login?sid=xiaomiio"}')
                if url.path == "/longPolling/loginUrl":
                    test.assertEqual(url.query["sid"], "xiaomiio")
                    return Response(b'{"code":0,"qr":"https://account.xiaomi.com/image","loginUrl":"https://eu.account.xiaomi.com/approve","lp":"https://eu.lp.account.xiaomi.com/poll"}')
                if url.host == "eu.lp.account.xiaomi.com":
                    return Response(json.dumps({
                        "code": 0, "location": "https://sts.api.io.mi.com/sts",
                        "passToken": "mock-pass", "userId": 123, "ssecurity": security,
                    }).encode())
                test.assertEqual(url.host, "sts.api.io.mi.com")
                return Response(cookies={"serviceToken": "mock-service"})

        client = Client()
        challenge = await account.XiaomiAccount.start_qr(client)
        self.assertEqual(challenge.image.host, "account.xiaomi.com")
        result = await account.XiaomiAccount.finish_qr(client, challenge, "de")
        self.assertEqual((result.user_id, result.pass_token), ("123", "mock-pass"))

    async def test_username_password_login_and_inventory_path(self):
        test = self
        class LoginClient:
            def get(self, url, **kwargs):
                if url.host == "account.xiaomi.com":
                    return Response(b'&&&START&&&{"qs":"q","_sign":"s","sid":"xiaomiio","callback":"cb"}')
                return Response(cookies={"userId": "uid", "passToken": "saved", "serviceToken": "session"})

            def post(self, url, **kwargs):
                test.assertEqual(str(url), "https://account.xiaomi.com/pass/serviceLoginAuth2")
                test.assertEqual(kwargs["data"]["user"], "name@example.test")
                test.assertEqual(kwargs["data"]["hash"], "5F4DCC3B5AA765D61D8327DEB882CF99")
                return Response(b'&&&START&&&' + json.dumps({
                    "location": "https://api.io.mi.com/auth", "ssecurity": base64.b64encode(b"secret").decode(),
                    "passToken": "saved",
                }).encode())

        client = await account.XiaomiAccount.login(LoginClient(), "name@example.test", "password", "de")
        self.assertEqual(client.user_id, "uid")
        self.assertEqual(client.pass_token, "saved")
        called = []
        async def request(path, values):
            called.append((path, values))
            return {"list": [{"did": "d"}]}
        client.request = request
        self.assertEqual(await client.devices(), {"list": [{"did": "d"}]})
        self.assertEqual(called, [("/v2/home/device_list_page", {})])

    async def test_login_challenge_uses_safe_typed_code(self):
        class Client:
            def get(self, *_args, **_kwargs):
                return Response(b'&&&START&&&{"qs":"q","_sign":"s","sid":"xiaomiio","callback":"cb"}')
            def post(self, *_args, **_kwargs):
                return Response(b'&&&START&&&{"captchaUrl":"/captcha"}')
        with self.assertRaises(account.AccountChallenge) as caught:
            await account.XiaomiAccount.login(Client(), "user", "secret", "cn")
        self.assertEqual(caught.exception.code, "captcha_required")
        self.assertNotIn("secret", str(caught.exception))

    async def test_login_diagnostic_uses_only_numeric_server_code(self):
        class Client:
            def get(self, *_args, **_kwargs):
                return Response(b'&&&START&&&{"qs":"q","_sign":"s","sid":"xiaomiio","callback":"cb"}')

            def post(self, *_args, **_kwargs):
                return Response(b'&&&START&&&{"code":70016,"description":"private mock value"}')

        with self.assertRaisesRegex(account.AccountError, r"^Xiaomi rejected sign-in \(code 70016\)$"):
            await account.XiaomiAccount.login(Client(), "mock-user", "mock-password", "de")

    async def test_verification_challenge_continuation(self):
        test = self
        class Client:
            def get(self, url, **kwargs):
                url = account.URL(url)
                if url.host == "account.xiaomi.com":
                    if url.path.endswith("serviceLogin"):
                        return Response(b'&&&START&&&{"qs":"q","_sign":"s","sid":"xiaomiio","callback":"cb"}')
                    if url.path.endswith("/identity/list"):
                        return Response(b'&&&START&&&{"flag":8}', cookies={"identity_session": "transient"})
                    test.assertEqual(url.path, "/identity/auth/verifyEmail")
                    test.assertEqual(url.query["_flag"], "8")
                    return Response(b'&&&START&&&{"maskedEmail":"m***@example.com"}')
                return Response(cookies={"userId": "uid", "passToken": "saved", "serviceToken": "session"})
            def post(self, url, **kwargs):
                url = account.URL(url)
                if url.path.endswith("serviceLoginAuth2"):
                    return Response(b'&&&START&&&' + json.dumps({
                        "notificationUrl": "https://account.xiaomi.com/fe/service/identity/authStart",
                        "ssecurity": base64.b64encode(b"secret").decode(), "passToken": "saved",
                    }).encode())
                if url.path.endswith("sendEmailTicket"):
                    return Response(b'&&&START&&&{"code":0}')
                test.assertEqual(url.path, "/identity/auth/verifyEmail")
                test.assertEqual(url.query["ticket"], "123456")
                return Response(b'&&&START&&&{"location":"https://api.io.mi.com/auth"}')
        with self.assertRaises(account.AccountChallenge) as caught:
            await account.XiaomiAccount.login(Client(), "user", "secret", "de")
        self.assertEqual(caught.exception.code, "verification_required")
        result = await caught.exception.continuation(Client(), "123456")
        self.assertEqual((result.user_id, result.pass_token), ("uid", "saved"))

    async def test_restore_and_send_encrypted_request(self):
        security = b"mock-session-security"
        path = "/v2/device/miss_get_vendor"
        test = self

        class Client:
            def get(self, url, **args):
                test.assertFalse(args["allow_redirects"])
                if url.host == "account.xiaomi.com":
                    test.assertEqual(args["cookies"], {"userId": "test", "passToken": "mock-pass"})
                    return Response(b"&&&START&&&" + json.dumps({
                        "location": "https://api.io.mi.com/auth",
                        "ssecurity": base64.b64encode(security).decode(),
                    }).encode())
                test.assertNotIn("cookies", args)
                return Response(cookies={"userId": "test", "serviceToken": "mock-service"})

            def post(self, url, *, data, cookies, allow_redirects, **_):
                test.assertEqual(url, f"https://de.api.io.mi.com/app{path}")
                test.assertFalse(allow_redirects)
                test.assertEqual(cookies, {"userId": "test", "serviceToken": "mock-service"})
                key = sha256(security + base64.b64decode(data["_nonce"])).digest()
                decoded = account._rc4(key, base64.b64decode(data["data"]))
                test.assertEqual(json.loads(decoded), {"did": "mock"})
                return Response(base64.b64encode(account._rc4(key, b'{"code":0,"result":{"ok":true}}')))

        client = account.XiaomiAccount(Client(), region="de", user_id="test", pass_token="mock-pass")
        self.assertEqual(await client.request(path, {"did": "mock"}), {"ok": True})

    async def test_c201_settings_signed_read_validates_partial_result(self):
        security = b"settings-test"
        test = self

        class Client:
            def post(self, url, *, data, **_):
                test.assertEqual(url, "https://de.api.io.mi.com/app/miotspec/prop/get")
                key = sha256(security + base64.b64decode(data["_nonce"])).digest()
                decoded = account._rc4(key, base64.b64decode(data["data"]))
                test.assertEqual(json.loads(decoded), {"params": [
                    {"did": "c201", "siid": 2, "piid": 3},
                    {"did": "c201", "siid": 4, "piid": 1},
                    {"did": "c201", "siid": 5, "piid": 1},
                    {"did": "c201", "siid": 8, "piid": 6},
                    {"did": "c201", "siid": 8, "piid": 3},
                    {"did": "c201", "siid": 3, "piid": 1},
                    {"did": "c201", "siid": 3, "piid": 2},
                    {"did": "c201", "siid": 3, "piid": 3},
                    {"did": "c201", "siid": 3, "piid": 4},
                ]})
                rows = [
                    {"did": "c201", "siid": 2, "piid": 3, "code": 0, "value": 2},
                    {"did": "c201", "siid": 4, "piid": 1, "code": 0, "value": False},
                    {"did": "other", "siid": 2, "piid": 3, "code": 0, "value": 1},
                    {"did": "c201", "siid": 2, "piid": 3, "code": -1, "value": 1},
                    {"did": "c201", "siid": 4, "piid": 1, "code": 0, "value": 1},
                    {"did": "c201", "siid": 3, "piid": 1, "code": 0, "value": 0},
                    {"did": "c201", "siid": 3, "piid": 2, "code": 0, "value": 128},
                    {"did": "c201", "siid": 3, "piid": 3, "code": 0, "value": True},
                    {"did": "c201", "siid": 3, "piid": 4, "code": 0, "value": -1},
                    {"did": "c201", "siid": 5, "piid": 1, "code": 0, "value": True},
                    {"did": "c201", "siid": 8, "piid": 6, "code": 0, "value": False},
                    {"did": "c201", "siid": 8, "piid": 3, "code": -1, "value": False},
                ]
                payload = json.dumps({"code": 0, "result": rows}).encode()
                return Response(base64.b64encode(account._rc4(key, payload)))

        client = account.XiaomiAccount(Client(), region="de", user_id="test", pass_token="mock")
        client._session = security, {}
        self.assertEqual(await client.read_c201_settings("c201"), {
            "night_shot": 2, "indicator_light": False,
            "sd_card_status": 0, "sd_card_total": 128,
            "motion_detection": True, "human_detection": False,
        })

    async def test_refuses_untrusted_sign_in_target_before_request(self):
        class Client:
            calls = 0

            def get(self, *args, **kwargs):
                self.calls += 1
                return Response(b'&&&START&&&{"location":"https://xiaomi.com.example.org/auth"}')

        client = Client()
        xiaomi = account.XiaomiAccount(client, region="cn", user_id="test", pass_token="mock-pass")
        with self.assertRaisesRegex(account.AccountError, "renewed authorization"):
            await xiaomi.request("/v2/device/miss_get_vendor", {})
        self.assertEqual(client.calls, 1)

    async def test_rejected_saved_authorization_is_distinct_from_timeout(self):
        class Rejected:
            def get(self, *_args, **_kwargs):
                response = Response()
                response.status = 401
                return response

        class Offline:
            def get(self, *_args, **_kwargs):
                raise TimeoutError

        config = {"region": "de", "user_id": "mock-user", "pass_token": "mock-pass"}
        with self.assertRaises(account.AccountAuthorizationError):
            await account.XiaomiAccount(Rejected(), **config).devices()
        with self.assertRaises(account.AccountError) as caught:
            await account.XiaomiAccount(Offline(), **config).devices()
        self.assertNotIsInstance(caught.exception, account.AccountAuthorizationError)
