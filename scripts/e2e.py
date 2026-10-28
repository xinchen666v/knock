#!/usr/bin/env python3
"""knock CLI 端到端回归测试。

不需要人值守：自动起两个 client、互发链接、互发消息、再验离线补投。
每次改完 client / protocol / server 都可以跑一遍，确认收发没坏。

用法:
    python scripts/e2e.py                 # 默认连 localhost:8080
    python scripts/e2e.py --server host:port
    python scripts/e2e.py --no-offline    # 跳过离线补投（服务端没起来时）

退出码: 0 = 全过, 1 = 有失败
"""

import argparse
import os
import re
import shutil
import subprocess
import sys
import threading
import time

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
BIN = os.path.join(ROOT, ".workbuddy-ai", "bin", "knock.exe")
TMP = os.path.join(ROOT, ".workbuddy-ai", "tmp")

INVITE_RE = re.compile(r"knock://\S+")


class Proc:
    """一个 client 进程。stdout 由后台线程逐字节收集，等待用正则匹配。"""

    def __init__(self, name, cfgdir, args, server=None):
        self.name = name
        self.buf = b""
        self.lock = threading.Lock()
        env = dict(os.environ)
        env["APPDATA"] = cfgdir          # 隔离身份：否则两个 client 共用一个 identity.json
        os.makedirs(cfgdir, exist_ok=True)
        cmd = [BIN] + args
        if server:
            cmd += ["-server", server]
        self.p = subprocess.Popen(
            cmd, cwd=ROOT, env=env,
            stdin=subprocess.PIPE, stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT, bufsize=0,
        )
        threading.Thread(target=self._read, daemon=True).start()

    def _read(self):
        while True:
            try:
                chunk = self.p.stdout.read(1)
            except Exception:
                return
            if not chunk:
                return
            with self.lock:
                self.buf += chunk

    def text(self):
        with self.lock:
            return self.buf.decode("utf-8", "replace")

    def write(self, s):
        try:
            self.p.stdin.write(s.encode())
            self.p.stdin.flush()
        except Exception:
            pass

    def wait_for(self, pattern, timeout=20):
        end = time.time() + timeout
        while time.time() < end:
            if re.search(pattern, self.text()):
                return True
            if self.p.poll() is not None:
                time.sleep(0.1)
                return bool(re.search(pattern, self.text()))
            time.sleep(0.03)
        return False

    def invite(self, timeout=20):
        if not self.wait_for(r"knock://\S+", timeout):
            return None
        return INVITE_RE.search(self.text()).group(0)

    def stop(self):
        try:
            self.p.kill()
        except Exception:
            pass

    def drain(self):
        """返回当前输出，并从缓冲区消费掉（用于分段断言）。"""
        with self.lock:
            s = self.buf.decode("utf-8", "replace")
            self.buf = b""
            return s


def qid_of(link):
    return link.rstrip("/").split("/")[-1].split("?")[0]


def build():
    os.makedirs(os.path.dirname(BIN), exist_ok=True)
    r = subprocess.run(["go", "build", "-o", BIN, "./client"], cwd=ROOT,
                       capture_output=True, text=True)
    if r.returncode != 0:
        print("[build] 失败:\n" + r.stderr)
        sys.exit(1)


class Runner:
    def __init__(self):
        self.failures = []
        self.step_no = 0

    def step(self, title, ok, procs=(), detail=""):
        self.step_no += 1
        mark = "PASS" if ok else "FAIL"
        print(f"  [{self.step_no:02d}] {mark}  {title}")
        if not ok:
            self.failures.append(title)
            for p in procs:
                print(f"       --- {p.name} stdout ---")
                for line in p.text().splitlines():
                    print("       " + line)
            if detail:
                print("       " + detail)
        return ok


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--server", default=None, help="服务器地址，缺省走 client 内置默认")
    ap.add_argument("--no-offline", action="store_true", help="跳过离线补投用例")
    args = ap.parse_args()

    build()
    if os.path.isdir(TMP):
        shutil.rmtree(TMP, ignore_errors=True)
    os.makedirs(TMP, exist_ok=True)

    R = Runner()
    a = b = None
    try:
        print("\n== 在线互通 ==")
        a = Proc("A", os.path.join(TMP, "cfgA"), ["chat"], args.server)
        la = a.invite()
        if not R.step("A 打印邀请链接", la is not None, [a]):
            return 1
        print(f"        L_A = {la}")

        b = Proc("B", os.path.join(TMP, "cfgB"), ["chat"], args.server)
        lb = b.invite()
        if not R.step("B 打印邀请链接", lb is not None, [a, b]):
            return 1
        print(f"        L_B = {lb}")

        if not R.step("A/B 链接互不相同", la != lb, [a, b]):
            return 1

        a.write(lb + "\n")
        b.write(la + "\n")
        ok = R.step("两端进入聊天", a.wait_for("已连接") and b.wait_for("已连接"), [a, b])
        if not ok:
            return 1

        a.drain(); b.drain()

        a.write("msg-a1\n")
        R.step("A -> B 送达", b.wait_for(r"msg-a1"), [a, b])
        b.write("msg-b1\n")
        R.step("B -> A 送达", a.wait_for(r"msg-b1"), [a, b])

        a.write("msg-a2\n")
        R.step("A -> B 第二条", b.wait_for(r"msg-a2"), [a, b])
        b.write("msg-b2\n")
        R.step("B -> A 第二条", a.wait_for(r"msg-b2"), [a, b])

        if not args.no_offline:
            print("\n== 离线补投 (M2) ==")
            qid_b = qid_of(lb)
            b.stop()
            b.p.wait(timeout=5)
            time.sleep(0.5)

            a.write("msg-offline\n")
            time.sleep(1.0)
            R.step("A 在 B 离线时发送不报错", "错误" not in a.drain(), [a])

            b = Proc("B2", os.path.join(TMP, "cfgB"), ["chat", "-queue", qid_b], args.server)
            if not R.step("B 用旧 qid 重连并打印链接", b.invite() is not None, [a, b]):
                return 1
            b.write(la + "\n")
            got = R.step("B 重连后收到离线消息", b.wait_for(r"msg-offline", timeout=15), [a, b])
            if not got:
                return 1

            a.drain(); b.drain()
            b.write("msg-after-replay\n")
            R.step("补投后双向仍可用", a.wait_for(r"msg-after-replay"), [a, b])

    finally:
        for p in (a, b):
            if p:
                p.stop()

    print()
    if R.failures:
        print(f"FAILED: {len(R.failures)} 项 —— " + " / ".join(R.failures))
        return 1
    print(f"ALL PASS: {R.step_no} 项")
    return 0


if __name__ == "__main__":
    sys.exit(main())
