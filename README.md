# Gala

**Gala** is an incremental static gallery generator for directory trees.
Masivelly parallel static html gallery generator

It reads an originals directory, creates a separate static website with square
thumbnails and medium previews, and links directly back to the original files.
The source tree is never modified.

![slopware warning](https://brontosaurusrex.github.io/media/slopware02.svg)

Warning: This software is ai generated (vibe coded) and since it reads / writes data on your disk, it could be potentialy dangerous.

## Build

    make
    mv gala ~/bin

## Usage

    gala photos

will generate gala-site next to the photos dir. Both folders should be visible to the web, since the galleries created will link to original media for download.

For a longer version read: [READMEAI.md](READMEAI.md).
