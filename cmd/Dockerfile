FROM k8s.gcr.io/build-image/debian-base:buster-v1.6.0
#e2fsprogs is for blkid
#util-linux is for findmnt
RUN clean-install util-linux e2fsprogs mount ca-certificates udev xfsprogs

COPY ./ananas /bin/
ENTRYPOINT ["/bin/ananas"]