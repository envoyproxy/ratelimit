package utils

type RedisError string

func (e RedisError) Error() string {
	return string(e)
}

func CheckError(err error) {
	if err != nil {
		panic(RedisError(err.Error()))
	}
}
