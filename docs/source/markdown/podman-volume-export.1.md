% podman-volume-export(1)

## NAME
podman\-volume\-export - Exports volume to external tar

## SYNOPSIS
**podman volume export** [*options*] *volume*

## DESCRIPTION

**podman volume export** exports the contents of a podman volume and saves it as a tarball
on the local machine. **podman volume export** writes to STDOUT by default and can be
redirected to a file using the `--output` flag.

Note: Following command is not supported by podman-remote.

**podman volume export [OPTIONS] VOLUME**

## OPTIONS

#### **--output**, **-o**=*file*

Write to a file, default is STDOUT

#### **--help**

Print usage statement


## EXAMPLES

```
$ podman volume export myvol --output myvol.tar

```

## SEE ALSO
podman-volume(1), podman-volume-import(1)
