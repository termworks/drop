oslo.direnv.nix_develop()

oslo.direnv.path_add("./")

oslo.env.set("TOP_HEAD", oslo.sys.pwd())

oslo.env.unset("GITHUB_TOKEN")

oslo.env.set("CGO_ENABLED", "0")

oslo.env.set_alias("_b", "make build")
oslo.env.set_alias("_c", "make compile")
oslo.env.set_alias("_r", "make run")
oslo.env.set_alias("_t", "make test")
oslo.env.set_alias("_v", "make verify")
oslo.env.set_alias("_i", "make install")
