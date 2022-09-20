# Bring your own image for debugging instructions

Dockerfile sometimes contains stages based on the lightweight image (e.g. `FROM scratch` or `FROM busybox`). 
It minimizes the result size but makes debugging hard because of lacking shell and tools.

`--image=<image-name>` flag of `buildg debug` enables you to bring your own image for debugging instructions. 
This allows interactive debugging on any stage even for ones based on `scratch`.

That image is usable on `exec` command using `--image` flag on it.
When this flag is specified, it launches a process on the rootfs of the image specified by `buildg debug --image=<image-name>`. 
The original rootfs of that stage is mounted at `/debugroot` (configurable via `--mountroot` flag) for allowing inspection.

## Example

Example Dockerfile:

```Dockerfile
FROM busybox AS dev
RUN echo hello > /hello

FROM scratch
COPY --from=dev /hello /
```

The above Dockerfile uses `scratch` and `busybox` as the base images but here we use `ubuntu:22.04` for debugging these stages.

```
$ buildg debug --image=ubuntu:22.04 /tmp/ctx
```

Here we launch a shell on line 2 with the rootfs of `ubuntu:22.04` instead of `busybox` (the original rootfs of that stage).

```
Filename: "Dockerfile"
 =>   1| FROM busybox AS dev
      2| RUN echo hello > /hello
      3| 
      4| FROM scratch
(buildg) next

...(omit)...

Filename: "Dockerfile"
      1| FROM busybox AS dev
 =>   2| RUN echo hello > /hello
      3| 
      4| FROM scratch
      5| COPY --from=dev /hello /
(buildg) exec --image
# cat /etc/os-release
PRETTY_NAME="Ubuntu 22.04.1 LTS"
NAME="Ubuntu"
VERSION_ID="22.04"
VERSION="22.04.1 LTS (Jammy Jellyfish)"
VERSION_CODENAME=jammy
ID=ubuntu
ID_LIKE=debian
HOME_URL="https://www.ubuntu.com/"
SUPPORT_URL="https://help.ubuntu.com/"
BUG_REPORT_URL="https://bugs.launchpad.net/ubuntu/"
PRIVACY_POLICY_URL="https://www.ubuntu.com/legal/terms-and-policies/privacy-policy"
UBUNTU_CODENAME=jammy
```

The original rootfs of that stage (`busybox`) is mounted at `/debugroot`.

```
# ls /debugroot/
bin  dev  etc  hello  home  proc  root	tmp  usr  var
```

We can launch a shell even on the `scratch`-based stage.

```
(buildg) b 5
(buildg) c

...(omit)...

INFO[2022-09-20T19:38:34+09:00] CACHED [stage-1 1/1] COPY --from=dev /hello / 
Breakpoint[0]: reached line: Dockerfile:5
Filename: "Dockerfile"
      2| RUN echo hello > /hello
      3| 
      4| FROM scratch
*=>   5| COPY --from=dev /hello /
(buildg) exec --image
# cat /debugroot/hello
hello
```
