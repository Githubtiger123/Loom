"""Loom transport adapter; KFP artifact paths may be files or directories.

Argo transports one deterministic tar.gz object. Never follow symlinks, extract
absolute paths, accept hard links/devices, or trust tarfile.extractall defaults.
"""
import gzip
import hashlib
import os
import pathlib
import shutil
import stat
import tarfile

ARTIFACT_MAX_FILES = 10000
ARTIFACT_MAX_BYTES = 10 * 1024**3


def artifact_digest(path):
    digest = hashlib.sha256()
    size = 0
    with open(path, "rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            size += len(block)
            if size > ARTIFACT_MAX_BYTES:
                raise ValueError("artifact exceeds transport limit")
            digest.update(block)
    return digest.hexdigest(), size


def pack_artifact(path):
    target = pathlib.Path(path)
    mode = target.lstat().st_mode
    if not (stat.S_ISREG(mode) or stat.S_ISDIR(mode)):
        raise ValueError("artifact must be a regular file or directory")
    kind = "DIRECTORY" if stat.S_ISDIR(mode) else "FILE"
    paths = [target]
    if kind == "DIRECTORY":
        for parent, directories, files in os.walk(target, followlinks=False):
            for name in sorted(directories + files):
                paths.append(pathlib.Path(parent) / name)
                if len(paths) > ARTIFACT_MAX_FILES:
                    raise ValueError("artifact has too many entries")
    paths.sort(key=lambda entry: entry.relative_to(target.parent).as_posix())
    archive_path = path + ".loom.tar.gz"
    size = 0
    with open(archive_path, "xb") as raw:
        with gzip.GzipFile(fileobj=raw, mode="wb", filename="", mtime=0) as compressed:
            with tarfile.open(fileobj=compressed, mode="w|") as archive:
                for entry in paths:
                    info = entry.lstat()
                    if not (stat.S_ISREG(info.st_mode) or stat.S_ISDIR(info.st_mode)):
                        raise ValueError("artifact contains links or special files")
                    if stat.S_ISREG(info.st_mode) and info.st_nlink != 1:
                        raise ValueError("artifact contains a hard-linked file")
                    relative = entry.relative_to(target).as_posix()
                    member_name = "data" if relative == "." else "data/" + relative
                    member = tarfile.TarInfo(member_name)
                    member.mode = 0o755 if stat.S_ISDIR(info.st_mode) else 0o644
                    member.type = tarfile.DIRTYPE if stat.S_ISDIR(info.st_mode) else tarfile.REGTYPE
                    if member.isfile():
                        member.size = info.st_size
                        size += info.st_size
                        if size > ARTIFACT_MAX_BYTES:
                            raise ValueError("artifact exceeds decoded limit")
                        # O_NOFOLLOW prevents a file-to-symlink race while the
                        # component's descendant processes are exiting.
                        fd = os.open(entry, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
                        with os.fdopen(fd, "rb") as stream:
                            current = os.fstat(stream.fileno())
                            if not stat.S_ISREG(current.st_mode) or current.st_nlink != 1 or (current.st_dev, current.st_ino, current.st_size) != (info.st_dev, info.st_ino, info.st_size):
                                raise ValueError("artifact changed during packaging")
                            archive.addfile(member, stream)
                    else:
                        archive.addfile(member)
    digest, stored_size = artifact_digest(archive_path)
    if kind == "DIRECTORY":
        shutil.rmtree(target)
    else:
        target.unlink()
    os.replace(archive_path, path)
    return {"sha256": digest, "size": stored_size, "kind": kind, "format": "TAR_GZIP_V1", "decodedSize": size, "entries": len(paths)}


def unpack_artifact(path, stored):
    if stored.get("format") != "TAR_GZIP_V1" or stored.get("kind") not in ("FILE", "DIRECTORY"):
        raise ValueError("artifact transport protocol mismatch")
    digest, size = artifact_digest(path)
    if digest != stored["sha256"] or size != stored["size"]:
        raise ValueError("artifact integrity mismatch")
    root = pathlib.Path(path + ".unpacked")
    root.mkdir(mode=0o700)
    seen, decoded_size = set(), 0
    with gzip.open(path, "rb") as compressed, tarfile.open(fileobj=compressed, mode="r|") as archive:
        for member in archive:
            relative = pathlib.PurePosixPath(member.name)
            if member.name in seen or len(seen) >= ARTIFACT_MAX_FILES or relative.is_absolute() or not relative.parts or relative.parts[0] != "data" or any(p in ("", ".", "..") for p in member.name.split("/")) or "\\" in member.name:
                raise ValueError("unsafe artifact archive entry")
            seen.add(member.name)
            destination = root.joinpath(*relative.parts)
            if member.isdir():
                destination.mkdir(parents=True, exist_ok=True)
            elif member.isfile():
                decoded_size += member.size
                if member.size < 0 or decoded_size > ARTIFACT_MAX_BYTES:
                    raise ValueError("artifact exceeds decoded limit")
                destination.parent.mkdir(parents=True, exist_ok=True)
                with archive.extractfile(member) as source, open(destination, "xb") as target:
                    shutil.copyfileobj(source, target, 1024 * 1024)
            else:
                raise ValueError("artifact contains links or special files")
        # Verify the gzip trailer, while bounding padding beyond the tar EOF.
        if len(compressed.read(1024 * 1024 + 1)) > 1024 * 1024:
            raise ValueError("artifact has excessive archive padding")
    payload = root / "data"
    if decoded_size != stored.get("decodedSize") or len(seen) != stored.get("entries") or (payload.is_dir() != (stored["kind"] == "DIRECTORY")):
        raise ValueError("artifact manifest mismatch")
    os.unlink(path)
    os.replace(payload, path)
    root.rmdir()
