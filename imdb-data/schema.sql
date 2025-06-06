-- Documented here https://developer.imdb.com/non-commercial-datasets/

CREATE TABLE IF NOT EXISTS title_akas (
    title_id text NOT NULL, -- a tconst, an alphanumeric unique identifier of the title
    ordering integer NOT NULL, -- a number to uniquely identify rows for a given titleId
    title text, -- the localized title
    region text, -- the region for this version of the title
    language text, -- the language of the title
    types text, -- Enumerated set of attributes for this alternative title. One or more of the following: "alternative", "dvd", "festival", "tv", "video", "working", "original", "imdbDisplay". New values may be added in the future without warning
    attributes text, -- Additional terms to describe this alternative title, not enumerated
    is_original_title bool -- 0: not original title; 1: original title
);

CREATE TABLE IF NOT EXISTS title_basics (
    tconst text NOT NULL, -- alphanumeric unique identifier of the title
    title_type text NOT NULL, -- the type/format of the title (e.g. movie, short, tvseries, tvepisode, video, etc)
    primary_title text NOT NULL, -- the more popular title / the title used by the filmmakers on promotional materials at the point of release
    original_title text NOT NULL, -- original title, in the original language
    is_adult bool NOT NULL, -- 0: non-adult title; 1: adult title
    start_year integer, -- represents the release year of a title. In the case of TV Series, it is the series start year
    end_year integer, -- TV Series end year. '\N' for all other title types
    runtime_minutes integer, -- primary runtime of the title, in minutes
    genres text -- includes up to three genres associated with the title
);

CREATE TABLE IF NOT EXISTS title_crew (
    tconst text NOT NULL, -- alphanumeric unique identifier of the title
    directors text, -- array of nconsts, director(s) of the given title
    writers text -- array of nconsts, writer(s) of the given title
);

CREATE TABLE IF NOT EXISTS title_episode (
    tconst text NOT NULL, -- alphanumeric identifier of episode
    parent_tconst text, -- alphanumeric identifier of the parent TV Series
    season_number integer, -- season number the episode belongs to
    episode_number integer -- episode number of the tconst in the TV series
);

CREATE TABLE IF NOT EXISTS title_principals (
    tconst text NOT NULL, -- alphanumeric unique identifier of the title
    ordering integer NOT NULL, -- a number to uniquely identify rows for a given titleId
    nconst text NOT NULL, -- alphanumeric unique identifier of the name/person
    category text NOT NULL, -- the category of job that person was in
    job text, -- the specific job title if applicable, else '\N'
    characters text -- the name of the character played if applicable, else '\N'
);

CREATE TABLE IF NOT EXISTS title_ratings (
    tconst text NOT NULL, -- alphanumeric unique identifier of the title
    average_rating real, -- weighted average of all the individual user ratings
    num_votes integer -- number of votes the title has received
);

CREATE TABLE IF NOT EXISTS name_basics (
    nconst text NOT NULL, -- alphanumeric unique identifier of the name/person
    primary_name text, -- name by which the person is most often credited
    birth_year integer, -- in YYYY format
    death_year integer, -- in YYYY format if applicable, else '\N'
    primary_profession text, -- the top-3 professions of the person
    known_for_titles text -- titles the person is known for
);
