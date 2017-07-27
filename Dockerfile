FROM golang:1.8-stretch
ADD bin/ratelimit* /bin/
CMD ["/bin/ratelimit"]
