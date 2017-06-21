FROM alpine:3.6
ADD bin/ratelimit* /bin/
CMD ["/bin/ratelimit"]
