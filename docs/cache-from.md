# Debug remotely cached build with `--cache-from`

BuildKit supports building images on remote hosts like Kubernetes.

Though, as of now, buildg doesn't support remote BuildKit host, you can inspect that remote build from the build cache.
BuildKit can emit the build cache to the remote location (e.g. registry).
Buildg can load and inspect the remote build cache.

## Example

Example Dockerfile:

```dockerfile
FROM ghcr.io/stargz-containers/ubuntu:20.04-org AS build1
RUN echo hello > /hello

FROM ghcr.io/stargz-containers/ubuntu:20.04-org AS build2
RUN echo hi > /hi

FROM ghcr.io/stargz-containers/ubuntu:20.04-org
COPY --from=build1 /hello /
COPY --from=build2 /hi /
RUN cat /hello /hi
```

Here we use Kind to build this Dockerfile.

```
$ kind create cluster
```

We use [Docker Buildx](https://github.com/docker/buildx) as the client CLI of BuildKit to build this Dockerfile on Kind cluster.

Buildx supports [`--driver kubernetes`](https://docs.docker.com/engine/reference/commandline/buildx_create/#driver) to use Kubernetes pods to build Dockerfile.

```
$ docker buildx create --driver kubernetes --use
```

You can use [`--cache-to`](https://docs.docker.com/engine/reference/commandline/buildx_build/#cache-to) option to export the build cache to the external location.
Here we export the build cache to `ghcr.io/ktock/myimagecache:kind`.
`mode=max` exports build cache for all stages.

```
$ docker buildx build --cache-to type=registry,ref=ghcr.io/ktock/myimagecache:debug,mode=max /tmp/buildctx/
```

> Note that BuildKit doesn't support exporting build cache on failed build (https://github.com/moby/buildkit/issues/2064).
> You can avoid this limitation and export the cache by fixing the Dockerfile to stop the build immediately before the failed step.
> We'll work on fixing BuildKit to support cache exports on failed builds as well.

Finally, you can load and inspect the build cache using buildg with `--cache-from` flag.
This allows inspecting steps without running them but leveraging the build cache loaded from the specified location.

```console
$ buildg debug --cache-from=ghcr.io/ktock/myimagecache:debug /tmp/buildctx/
WARN[2022-06-03T15:24:03+09:00] using host network as the default            
#1 [internal] load build definition from Dockerfile
#1 transferring dockerfile: 319B done
#1 DONE 0.1s

#2 [internal] load .dockerignore
#2 transferring context: 2B done
#2 DONE 0.1s

#3 [internal] load metadata for ghcr.io/stargz-containers/ubuntu:20.04-org
#3 ...

#4 [auth] stargz-containers/ubuntu:pull token for ghcr.io
#4 DONE 0.0s

#3 [internal] load metadata for ghcr.io/stargz-containers/ubuntu:20.04-org
#3 DONE 2.7s

#5 [build2 1/2] FROM ghcr.io/stargz-containers/ubuntu:20.04-org@sha256:adf73ca014822ad8237623d388cedf4d5346aa72c270c5acc01431cc93e18e2d
#5 resolve ghcr.io/stargz-containers/ubuntu:20.04-org@sha256:adf73ca014822ad8237623d388cedf4d5346aa72c270c5acc01431cc93e18e2d 0.0s done
#5 DONE 0.0s

#6 importing cache manifest from ghcr.io/ktock/myimagecache:debug
#6 ...

#5 [build2 1/2] FROM ghcr.io/stargz-containers/ubuntu:20.04-org@sha256:adf73ca014822ad8237623d388cedf4d5346aa72c270c5acc01431cc93e18e2d
#5 DONE 0.0s

#7 [auth] ktock/myimagecache:pull token for ghcr.io
#7 DONE 0.0s

#6 importing cache manifest from ghcr.io/ktock/myimagecache:debug
#6 DONE 2.1s
INFO[2022-06-03T15:24:08+09:00] debug session started. type "help" for command reference. 
Filename: "Dockerfile"
 =>   1| FROM ghcr.io/stargz-containers/ubuntu:20.04-org AS build1
      2| RUN echo hello > /hello
      3| 
 =>   4| FROM ghcr.io/stargz-containers/ubuntu:20.04-org AS build2
      5| RUN echo hi > /hi
      6| 
 =>   7| FROM ghcr.io/stargz-containers/ubuntu:20.04-org
      8| COPY --from=build1 /hello /
      9| COPY --from=build2 /hi /
     10| RUN cat /hello /hi
(buildg) break 5
(buildg) break 10
(buildg) continue

#5 [build2 1/2] FROM ghcr.io/stargz-containers/ubuntu:20.04-org@sha256:adf73ca014822ad8237623d388cedf4d5346aa72c270c5acc01431cc93e18e2d
#5 sha256:57671312ef6fdbecf340e5fed0fb0863350cd806c92b1fdd7978adbd02afc5c3 0B / 851B 0.2s
#5 sha256:5e9250ddb7d0fa6d13302c7c3e6a0aa40390e42424caed1e5289077ee4054709 0B / 187B 0.2s
#5 sha256:345e3491a907bb7c6f1bdddcf4a94284b8b6ddd77eb7d93f09432b17b20f2bbe 0B / 28.54MB 0.2s
#5 sha256:57671312ef6fdbecf340e5fed0fb0863350cd806c92b1fdd7978adbd02afc5c3 851B / 851B 0.4s done
#5 sha256:5e9250ddb7d0fa6d13302c7c3e6a0aa40390e42424caed1e5289077ee4054709 187B / 187B 0.4s done
#5 sha256:345e3491a907bb7c6f1bdddcf4a94284b8b6ddd77eb7d93f09432b17b20f2bbe 0B / 28.54MB 5.3s
#5 sha256:345e3491a907bb7c6f1bdddcf4a94284b8b6ddd77eb7d93f09432b17b20f2bbe 5.24MB / 28.54MB 5.6s
#5 sha256:345e3491a907bb7c6f1bdddcf4a94284b8b6ddd77eb7d93f09432b17b20f2bbe 12.58MB / 28.54MB 5.7s
#5 sha256:345e3491a907bb7c6f1bdddcf4a94284b8b6ddd77eb7d93f09432b17b20f2bbe 20.97MB / 28.54MB 5.9s
#5 sha256:345e3491a907bb7c6f1bdddcf4a94284b8b6ddd77eb7d93f09432b17b20f2bbe 28.54MB / 28.54MB 6.0s done
#5 extracting sha256:345e3491a907bb7c6f1bdddcf4a94284b8b6ddd77eb7d93f09432b17b20f2bbe
#5 extracting sha256:345e3491a907bb7c6f1bdddcf4a94284b8b6ddd77eb7d93f09432b17b20f2bbe 0.6s done
#5 extracting sha256:57671312ef6fdbecf340e5fed0fb0863350cd806c92b1fdd7978adbd02afc5c3
#5 extracting sha256:57671312ef6fdbecf340e5fed0fb0863350cd806c92b1fdd7978adbd02afc5c3 0.2s done
#5 extracting sha256:5e9250ddb7d0fa6d13302c7c3e6a0aa40390e42424caed1e5289077ee4054709
#5 extracting sha256:5e9250ddb7d0fa6d13302c7c3e6a0aa40390e42424caed1e5289077ee4054709 0.2s done
#5 DONE 21.2s
INFO[2022-06-03T15:24:29+09:00] CACHED [build2 2/2] RUN echo hi > /hi        
Breakpoint[0]: reached line: Dockerfile:5
Filename: "Dockerfile"
      2| RUN echo hello > /hello
      3| 
      4| FROM ghcr.io/stargz-containers/ubuntu:20.04-org AS build2
*=>   5| RUN echo hi > /hi
      6| 
      7| FROM ghcr.io/stargz-containers/ubuntu:20.04-org
      8| COPY --from=build1 /hello /
INFO[2022-06-03T15:24:29+09:00] CACHED [build1 2/2] RUN echo hello > /hello  
(buildg) exec cat /hi
hi
(buildg) continue
INFO[2022-06-03T15:24:42+09:00] CACHED [stage-2 2/4] COPY --from=build1 /hello / 
INFO[2022-06-03T15:24:42+09:00] CACHED [stage-2 3/4] COPY --from=build2 /hi / 
INFO[2022-06-03T15:24:42+09:00] CACHED [stage-2 4/4] RUN cat /hello /hi      
Breakpoint[1]: reached line: Dockerfile:10
Filename: "Dockerfile"
      7| FROM ghcr.io/stargz-containers/ubuntu:20.04-org
      8| COPY --from=build1 /hello /
      9| COPY --from=build2 /hi /
*=>  10| RUN cat /hello /hi
(buildg) exec cat /hello /hi
hello
hi
(buildg) q
```
